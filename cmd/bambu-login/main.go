package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/chrisjohnson/printer-dashboard/internal/printers/bambu"
)

func main() {
	emailFlag := flag.String("email", "", "Bambu account email (pre-fill, password always prompted)")
	flag.Parse()

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║      Bambu Lab Cloud Token Utility                  ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Println("║  Gets a JWT token for Bambu Cloud printers.         ║")
	fmt.Println("║  No LAN mode or developer mode needed.              ║")
	fmt.Println("║  Token lasts ~3 months.                             ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	// Keep log output so we can see debug info from the bambu package
	// during the 2FA flow.

	email := *emailFlag
	password := ""
	if email == "" {
		email = prompt("Bambu account email: ")
	} else {
		fmt.Printf("Bambu account email: %s\n", email)
	}
	password = prompt("Bambu account password: ")
	region := promptOptional("Region [global]: ", "global")

	cloud := bambu.NewBambuCloudClient(region)
	tokenPath := bambu.DefaultTokenPath(email)
	cloud.SetTokenFile(tokenPath)

	fmt.Println("\nLogging in...")
	err := cloud.Login(email, password, func() (string, error) {
		fmt.Print("\n📧 Verification code sent to your email.")
		fmt.Print("\n   Check inbox (and spam) for the 6-digit code.\n")
		return prompt("Enter code: "), nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Login failed: %v\n", err)
		if strings.Contains(err.Error(), "Incorrect account") {
			fmt.Println("\n💡 If you use Google SSO, choose option 2 instead.")
		}
		os.Exit(1)
	}

	printSuccess(cloud)
}

// ---------------------------------------------------------------------------
// Option 2: Interactive browser-based token extraction
// ---------------------------------------------------------------------------

func loginWithBrowser() {
	region := promptOptional("Region [global]: ", "global")

	// Find a free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Could not find a free port: %v\n", err)
		fmt.Println("\nFalling back to manual token entry.")
		manualTokenEntry(region)
		return
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// Channel to receive the token from the HTTP handler
	tokenCh := make(chan string, 1)

	// Setup HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveCapturePage(w, r, port)
	})
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		handleCallback(w, r, tokenCh)
	})
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		handleSubmit(w, r, tokenCh)
	})
	mux.HandleFunc("/console", func(w http.ResponseWriter, r *http.Request) {
		consoleSnippet := fmt.Sprintf(
			`fetch('http://127.0.0.1:%d/callback?token='+encodeURIComponent(Object.values(localStorage).find(v=>v.startsWith('eyJ'))))`,
			port)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(consoleSnippet + "\n"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: mux}

	// Start server
	go srv.Serve(listener)

	// Capture Ctrl+C to clean up
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	// Open browser tabs
	captureURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
	bambuURL := "https://e.bambulab.com"

	fmt.Println()
	fmt.Println("🚀 Opening your browser...")
	fmt.Println()
	openBrowser(bambuURL)
	time.Sleep(800 * time.Millisecond)
	openBrowser(captureURL)

	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║  👆 Check your browser — a capture page should be open.    ║")
	fmt.Println("║                                                             ║")
	fmt.Println("║  1. Log in at e.bambulab.com with Google SSO               ║")
	fmt.Println("║  2. On the capture page, drag the bookmarklet to your      ║")
	fmt.Println("║     bookmarks bar, then click it on e.bambulab.com         ║")
	fmt.Println("║  3. Or paste this one-liner into the DevTools Console:     ║")
	fmt.Println("║     (F12 → Console → Ctrl+V → Enter)                       ║")
	fmt.Println("║                                                             ║")
	fmt.Printf("║  (already on the capture page — scroll to Step 3)           ║\n")
	fmt.Println("║                                                             ║")
	fmt.Println("║  Token will auto-capture — no manual copying needed!       ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Wait for token or Ctrl+C
	select {
	case token := <-tokenCh:
		// Close the server
		srv.Close()
		stop()

		token = strings.TrimSpace(token)
		if token == "" {
			fmt.Println("\n❌ No token received.")
			os.Exit(1)
		}

		fmt.Println("\n✅ Token captured from browser! Validating...")
		validateAndSave(token, region)

	case <-ctx.Done():
		fmt.Println("\n\nCancelled.")
		srv.Close()
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// HTTP handlers for the capture page
// ---------------------------------------------------------------------------

// serveCapturePage renders the interactive token capture page.
func serveCapturePage(w http.ResponseWriter, r *http.Request, port int) {
	bookmarkletJS := url.QueryEscape(fmt.Sprintf(
		`(function(){var k=Object.keys(localStorage);for(var i=0;i<k.length;i++){var v=localStorage.getItem(k[i]);if(v&&v.length>100&&v.startsWith('eyJ')){var t=v;fetch('http://127.0.0.1:%d/callback?token='+encodeURIComponent(t)).then(function(){document.body.innerHTML='<h1>✅ Token captured!</h1><p>You can close this tab.</p>'})['catch'](function(){})['finally'](function(){window.location.href='http://127.0.0.1:%d/callback?token='+encodeURIComponent(t)});return}}alert('No Bambu token found in localStorage. Try logging in at e.bambulab.com first.')})()`,
		port, port))

	consoleSnippet := fmt.Sprintf(
		`fetch('http://127.0.0.1:%d/callback?token='+encodeURIComponent(Object.values(localStorage).find(v=>v.startsWith('eyJ'))))`,
		port)

	pageTmpl := template.Must(template.New("capture").Parse(captureHTML))
	pageTmpl.Execute(w, map[string]any{
		"Port":            port,
		"BookmarkletJS":   template.URL("javascript:" + bookmarkletJS),
		"ConsoleSnippet":  consoleSnippet,
		"BambuURL":        "https://e.bambulab.com",
		"CallbackURL":     fmt.Sprintf("http://127.0.0.1:%d/callback", port),
	})
}

// handleCallback receives the token via GET /callback?token=...
func handleCallback(w http.ResponseWriter, r *http.Request, tokenCh chan<- string) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Missing token parameter", http.StatusBadRequest)
		return
	}
	// Send the token to the waiting CLI
	select {
	case tokenCh <- token:
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<h1>✅ Token captured!</h1><p>You can close this tab and return to the terminal.</p>"))
	default:
		http.Error(w, "Already received a token", http.StatusConflict)
	}
}

// handleSubmit receives the token via POST /submit (form-encoded or JSON)
func handleSubmit(w http.ResponseWriter, r *http.Request, tokenCh chan<- string) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var token string
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var data struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		token = data.Token
	} else {
		token = r.FormValue("token")
	}

	if token == "" {
		http.Error(w, "Missing token", http.StatusBadRequest)
		return
	}

	select {
	case tokenCh <- token:
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<h1>✅ Token captured!</h1><p>You can close this tab and return to the terminal.</p>"))
	default:
		http.Error(w, "Already received a token", http.StatusConflict)
	}
}

// ---------------------------------------------------------------------------
// Token validation and saving
// ---------------------------------------------------------------------------

func validateAndSave(token, region string) {
	cloud := bambu.NewBambuCloudClient(region)

	// Read info from the token
	email := ""
	if jp, err := bambu.ParseJWT(token); err == nil && jp != nil {
		email = jp.Email
		fmt.Printf("   Token belongs to: %s\n", jp.Email)
		fmt.Printf("   Expires: %s\n", time.Unix(jp.Exp, 0).Format("2006-01-02 15:04:05"))
	}

	// Determine token path
	tokenPath := bambu.DefaultTokenPath(email)
	if email == "" {
		tokenPath = bambu.DefaultTokenDir + "/bambu_token_sso.json"
	}
	cloud.SetTokenFile(tokenPath)

	// Validate and save
	if err := cloud.LoginWithToken(token); err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Token validation failed: %v\n", err)
		fmt.Println("   The token may be expired or invalid. Try re-extracting.")
		os.Exit(1)
	}

	printSuccess(cloud)
}

func printSuccess(cloud *bambu.BambuCloudClient) {
	fmt.Println("\n✅ Token obtained and saved!")

	exp := cloud.TokenExpiry()
	if !exp.IsZero() {
		left := time.Until(exp)
		fmt.Printf("   Token expires: %s (in %s)\n",
			exp.Format("2006-01-02 15:04:05"),
			friendlyDuration(left))
	}

	// Fetch device list
	devices, err := cloud.GetDevices()
	if err != nil {
		fmt.Printf("\n⚠️  Could not fetch device list: %v\n", err)
		fmt.Println("   (Your token is still valid — this might be a network issue.)")
	} else {
		fmt.Printf("\n📋 Bound printers (%d):\n", len(devices))
		for _, d := range devices {
			fmt.Printf("   • %s\n", d.Name)
			fmt.Printf("     Serial: %s\n", d.DevID)
			fmt.Printf("     Model:  %s\n", d.DevProductName)
			fmt.Printf("     Online: %v\n", d.Online)
			fmt.Println()
		}
	}

	fmt.Println("📝 Token saved to disk. The server will auto-load it on restart.")
	fmt.Println()
	fmt.Println("   Add this to config.yaml's bambu_account section:")
	fmt.Printf("   user_id: %s\n", cloud.UserID())
	fmt.Println("   (token is already on disk — no need to copy it into yaml)")
	fmt.Println()
	fmt.Println("🚀 Start the dashboard:")
	fmt.Println("   go run .")
}

// ---------------------------------------------------------------------------
// Manual fallback (if browser can't open)
// ---------------------------------------------------------------------------

func manualTokenEntry(region string) {
	fmt.Println("\n📋 Step-by-step: Get your token from the browser")
	fmt.Println()
	fmt.Println("  1. Open https://e.bambulab.com in your browser")
	fmt.Println("  2. Sign in with Google (or your usual method)")
	fmt.Println("  3. Open Developer Tools (F12 or Cmd+Option+I)")
	fmt.Println("  4. Go to the Console tab")
	fmt.Println("  5. Copy-paste and run:")
	fmt.Println()
	fmt.Println("     Object.values(localStorage).find(v => v.startsWith('eyJ'))")
	fmt.Println()
	fmt.Println("  6. Copy the result (the long string starting with eyJ)")
	fmt.Println()

	token := prompt("Paste the JWT token value: ")
	token = strings.TrimSpace(token)

	if !strings.HasPrefix(token, "eyJ") {
		fmt.Println("\n⚠️  That doesn't look like a JWT token (should start with 'eyJ...')")
		retry := prompt("Try again? [Y/n]: ")
		if retry != "n" && retry != "N" {
			manualTokenEntry(region)
		}
		os.Exit(1)
	}

	validateAndSave(token, region)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

func friendlyDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) {
	// Try 'open' on macOS, 'xdg-open' on Linux, 'start' on Windows
	cmds := [][]string{
		{"open", url},
		{"xdg-open", url},
		{"cmd", "/c", "start", url},
	}
	for _, cmd := range cmds {
		if err := exec.Command(cmd[0], cmd[1:]...).Start(); err == nil {
			return
		}
	}
	// If all fail, just print the URL
	fmt.Printf("   Please open this URL in your browser: %s\n", url)
}

// ---------------------------------------------------------------------------
// Embedded HTML for the capture page
// ---------------------------------------------------------------------------

const captureHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Bambu Token Capture</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: #f5f5f7;
    color: #1d1d1f;
    line-height: 1.6;
    padding: 40px 20px;
    display: flex;
    justify-content: center;
  }
  .container { max-width: 680px; width: 100%; }
  h1 { font-size: 28px; font-weight: 700; margin-bottom: 8px; }
  .subtitle { color: #6e6e73; font-size: 16px; margin-bottom: 32px; }
  .step {
    background: #fff;
    border-radius: 12px;
    padding: 24px;
    margin-bottom: 16px;
    box-shadow: 0 1px 3px rgba(0,0,0,0.08);
  }
  .step-number {
    display: inline-block;
    width: 28px; height: 28px;
    background: #0071e3;
    color: #fff;
    border-radius: 50%;
    text-align: center;
    line-height: 28px;
    font-size: 14px;
    font-weight: 600;
    margin-right: 10px;
  }
  .step-title { font-size: 18px; font-weight: 600; margin-bottom: 12px; }
  .step-title .step-number { vertical-align: middle; }
  .btn {
    display: inline-block;
    padding: 12px 24px;
    border-radius: 8px;
    font-size: 15px;
    font-weight: 500;
    cursor: pointer;
    text-decoration: none;
    border: none;
    transition: background 0.2s;
  }
  .btn-primary {
    background: #0071e3;
    color: #fff;
  }
  .btn-primary:hover { background: #0064cc; }
  .btn-secondary {
    background: #e8e8ed;
    color: #1d1d1f;
  }
  .btn-secondary:hover { background: #d1d1d6; }
  .bookmarklet {
    display: inline-block;
    padding: 10px 20px;
    background: #ffd60a;
    color: #1d1d1f;
    border-radius: 8px;
    font-size: 15px;
    font-weight: 600;
    text-decoration: none;
    cursor: move;
    border: 2px dashed #d4a800;
  }
  .bookmarklet:hover { background: #ffc600; }
  code {
    background: #f0f0f5;
    padding: 2px 6px;
    border-radius: 4px;
    font-size: 14px;
    word-break: break-all;
  }
  pre {
    background: #1d1d1f;
    color: #f5f5f7;
    padding: 16px;
    border-radius: 8px;
    font-size: 13px;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-all;
    margin: 8px 0;
  }
  .copy-btn {
    font-size: 13px;
    color: #0071e3;
    background: none;
    border: none;
    cursor: pointer;
    text-decoration: underline;
  }
  .copy-btn:hover { color: #0064cc; }
  input[type="text"] {
    width: 100%;
    padding: 12px;
    border: 1px solid #d2d2d7;
    border-radius: 8px;
    font-size: 14px;
    font-family: monospace;
    margin: 8px 0;
  }
  input[type="text"]:focus {
    outline: none;
    border-color: #0071e3;
    box-shadow: 0 0 0 3px rgba(0,113,227,0.2);
  }
  .status {
    display: none;
    padding: 16px;
    border-radius: 8px;
    margin-top: 16px;
    font-weight: 500;
  }
  .status.success {
    display: block;
    background: #d1fae5;
    color: #065f46;
    border: 1px solid #a7f3d0;
  }
  .status.error {
    display: block;
    background: #fee2e2;
    color: #991b1b;
    border: 1px solid #fecaca;
  }
  hr { border: none; border-top: 1px solid #e5e5ea; margin: 24px 0; }
</style>
</head>
<body>
<div class="container">
  <h1>🔑 Bambu Token Capture</h1>
  <p class="subtitle">Extract your Bambu Lab JWT token without touching Developer Tools.</p>

  <!-- Step 1 -->
  <div class="step">
    <div class="step-title"><span class="step-number">1</span> Log in to Bambu Lab</div>
    <p style="margin-bottom:12px;">Open <strong>e.bambulab.com</strong> and sign in with Google SSO if you haven't already.</p>
    <a href="{{.BambuURL}}" target="_blank" class="btn btn-primary">Open e.bambulab.com ↗</a>
  </div>

  <!-- Step 2: Bookmarklet -->
  <div class="step">
    <div class="step-title"><span class="step-number">2</span> Drag this bookmarklet to your bookmarks bar</div>
    <p style="margin-bottom:12px;">Drag the yellow button below to your bookmarks bar, then click it while on <strong>e.bambulab.com</strong>.</p>
    <a href="{{.BookmarkletJS}}" class="bookmarklet">🔑 Extract Token</a>
    <p style="margin-top:12px;color:#6e6e73;font-size:14px;">
      If you can't see your bookmarks bar, press <kbd>Cmd+Shift+B</kbd> (Mac) or <kbd>Ctrl+Shift+B</kbd> (Windows/Linux).
    </p>
  </div>

  <!-- Step 3: One-liner fallback -->
  <div class="step">
    <div class="step-title"><span class="step-number">3</span> Or use the Console one-liner</div>
    <p style="margin-bottom:8px;">If the bookmarklet doesn't work:</p>
    <ol style="margin-left:20px;margin-bottom:12px;">
      <li>Go to <strong>e.bambulab.com</strong> (make sure you're logged in)</li>
      <li>Press <kbd>F12</kbd> or <kbd>Cmd+Option+I</kbd> to open DevTools</li>
      <li>Go to the <strong>Console</strong> tab</li>
      <li>Paste this one-liner and press <kbd>Enter</kbd>:</li>
    </ol>
    <pre id="consoleSnippet">{{.ConsoleSnippet}}</pre>
    <button class="copy-btn" onclick="copySnippet()">📋 Copy to clipboard</button>
    <div id="copyStatus" style="display:none;color:#065f46;font-size:13px;margin-top:4px;">✅ Copied!</div>
  </div>

  <!-- Step 4: Manual paste -->
  <div class="step">
    <div class="step-title"><span class="step-number">4</span> Manual paste (last resort)</div>
    <p style="margin-bottom:8px;">If all else fails, paste the token manually:</p>
    <input type="text" id="manualToken" placeholder="eyJhbGciOiJSUzI1NiIs..." />
    <button class="btn btn-secondary" onclick="submitManual()">Submit Token</button>
  </div>

  <div id="status" class="status"></div>

  <hr />
  <p style="color:#6e6e73;font-size:14px;">
    ⏳ Waiting for token from browser... Once captured, check your terminal.
  </p>
</div>

<script>
function showStatus(msg, type) {
  const el = document.getElementById('status');
  el.textContent = msg;
  el.className = 'status ' + type;
}

function copySnippet() {
  const el = document.getElementById('consoleSnippet');
  navigator.clipboard.writeText(el.textContent).then(() => {
    document.getElementById('copyStatus').style.display = 'inline';
    setTimeout(() => document.getElementById('copyStatus').style.display = 'none', 2000);
  });
}

function submitManual() {
  const token = document.getElementById('manualToken').value.trim();
  if (!token) { showStatus('Please enter a token.', 'error'); return; }
  if (!token.startsWith('eyJ')) { showStatus('Doesn\'t look like a JWT (should start with eyJ...).', 'error'); return; }
  showStatus('Submitting...', 'success');
  fetch('/submit', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token: token })
  }).then(r => {
    if (r.ok) {
      showStatus('✅ Token captured! Check your terminal.', 'success');
    } else {
      showStatus('❌ Failed to submit token.', 'error');
    }
  }).catch(e => {
    showStatus('❌ Error: ' + e.message, 'error');
  });
}

// Check if we were redirected back from the bookmarklet (token in URL)
const params = new URLSearchParams(window.location.search);
if (params.get('token')) {
  showStatus('✅ Token captured! Check your terminal.', 'success');
}
</script>
</body>
</html>`


