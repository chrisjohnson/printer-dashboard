package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/chrisjohnson/printer-dashboard/internal/printers/bambu"
)

func main() {
	fmt.Println("=== Bambu Lab Cloud Token Utility ===")
	fmt.Println("This tool logs in to your Bambu Lab account and gets a JWT token")
	fmt.Println("for use with the printer dashboard (no LAN mode / dev mode needed).")
	fmt.Println()

	// Get credentials
	email := prompt("Bambu account email: ")
	password := prompt("Bambu account password: ")
	region := promptOptional("Region [global]: ", "global")

	// Attempt login
	cloud := bambu.NewBambuCloudClient(region)
	fmt.Println("\nLogging in...")

	err := cloud.Login(email, password, func() (string, error) {
		fmt.Print("\n📧 A verification code was sent to your email.")
		fmt.Print("\n   Check your inbox (and spam folder) for a 6-digit code.")
		return prompt("Enter verification code: "), nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Login failed: %v\n", err)
		fmt.Println("\nPossible issues:")
		fmt.Println("  - Check your email/password are correct")
		fmt.Println("  - If 2FA is not working, try logging into https://e.bambulab.com first")
		os.Exit(1)
	}

	fmt.Println("\n✅ Login successful!")
	fmt.Printf("   User ID: %s\n", cloud.UserID())
	fmt.Printf("   Token:   %s...\n", cloud.Token()[:min(50, len(cloud.Token()))])

	// Also fetch device list
	devices, err := cloud.GetDevices()
	if err != nil {
		fmt.Printf("\n⚠️  Could not fetch device list: %v\n", err)
	} else {
		fmt.Printf("\n📋 Bound printers (%d):\n", len(devices))
		for _, d := range devices {
			fmt.Printf("   - %s (serial: %s, model: %s, online: %v)\n",
				d.Name, d.DevID, d.DevProductName, d.Online)
		}
	}

	// Output as JSON for easy config
	result := struct {
		Token  string `json:"token"`
		UserID string `json:"user_id"`
		Region string `json:"region"`
	}{
		Token:  cloud.Token(),
		UserID: cloud.UserID(),
		Region: region,
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println("\n📝 Add this to your config.yaml under 'bambu_account':")
	fmt.Println(string(data))
	fmt.Println("\n(Remove the email/password fields if using the token.)")
}

func prompt(msg string) string {
	fmt.Print(msg)
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}

func promptOptional(msg, defaultVal string) string {
	val := prompt(msg)
	if val == "" {
		return defaultVal
	}
	return val
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
