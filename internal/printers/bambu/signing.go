package bambu

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
)

// Command signing for post-2024 Bambu firmware.
//
// Firmware released after roughly January 2025 cryptographically verifies
// every command in the "print" namespace. An unsigned frame is rejected with
// err_code 84033543 ("mqtt message verify failed"). The check is satisfied by
// a "header" envelope carrying an RSA-SHA256 signature over the exact JSON
// bytes of the payload, made with a certificate the printer trusts.
//
// The certificate and key below are Bambu's own shared app credentials, made
// public in January 2025 when they were extracted from Bambu Connect and
// shipped embedded in every copy of it. They are not secret. Developer Mode is
// not involved: it only makes the header optional.
//
// The printer must be told to trust the certificate first, via
// security.app_cert_install. That store lives in printer RAM and is cleared by
// a reboot, so it has to be re-installed on every connection.

// The CRL is deliberately self-minted with a matching issuer CN. The printer
// requires the crl field to be non-empty (an empty one answers
// "crl value is null") but does not validate its signature.
const (
	embeddedAppCertPEM = `-----BEGIN CERTIFICATE-----
MIIDXTCCAkWgAwIBAgIRAO48rAcSzurNqLf7xC50uiwwDQYJKoZIhvcNAQELBQAw
JjEkMCIGA1UEAwwbR0xPRjM4MTM3MzQwODkuYmFtYnVsYWIuY29tMB4XDTI0MTIx
MTA5MjkyMFoXDTI1MTIxMjA5MjkyMFowTDEkMCIGA1UEChMbR0xPRjM4MTM3MzQw
ODktNTI0YTM3YzgwMDAwMSQwIgYDVQQDExtHTE9GMzgxMzczNDA4OS01MjRhMzdj
ODAwMDAwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQDQNp2NfkajwcWH
PIqosa08P1ZwETPr1veZCMqieQxWtYw97wp+JCxX4yBrBcAwid7o7PHI9KQVzPRM
f0uXspaDUdSljrfJ/YwGEz7+GJz4+ml1UbWXBePyzXW1+N2hIGGn7BcNuA0v8rMY
uvVgiIIQNjLErgGcCWmMHLwsMMQ7LNprUZZKsSNB4HaQDH7cQZmYBN/O45np6l+K
VuLdzXdDpZcOM7bNO6smev822WPGDuKBo1iVfQbUe10X4dCNwkBR3QGpScVvg8gg
tRYZDYue/qc4Xaj806RZPttknWfxdvfZgoOmAiwnyQ5K3+mzNYHgQZAOC2ydkK4J
s+ZizK3lAgMBAAGjYDBeMA4GA1UdDwEB/wQEAwIDuDAMBgNVHRMBAf8EAjAAMB0G
A1UdDgQWBBTbM6dbfGu7o6o1IU59QyDzMcexjzAfBgNVHSMEGDAWgBTCydEtLumS
2pknAxmjOizTHKwImzANBgkqhkiG9w0BAQsFAAOCAQEAmmD3Fu37vgw4qr/Dgr15
FSdoCuVAZPD7I5FwcBlPH98TJ0hNUtnDVxkJ0pde8ZcQdYFkfYFNnX+7f06ps/TY
CtchEAlx9cXBfBnImO4mB2Y89uRh7HRA2BiUmme4Xjy5P3qyvOnx2lIiH2hFyXJ0
6N8UcBEviZTZd+D6FR5TJ8aNOhCwktutsrwKeSj4jrIWSD0vPlkQTbxUrm6x+7/i
JBwOsMNA5UB+SZxAn8BtcvzpxHaj1l3WRddZcykTfz6k8fuQfJCdp1aN47guLXWt
HTDvXeOlXpDStOlIwwMvh2i42ZaLas2C2B8rrX6pMmzazJLZcth8ZIyhfuB1WcMv
AQ==
-----END CERTIFICATE-----`
	embeddedAppKeyPEM  = `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDQNp2NfkajwcWH
PIqosa08P1ZwETPr1veZCMqieQxWtYw97wp+JCxX4yBrBcAwid7o7PHI9KQVzPRM
f0uXspaDUdSljrfJ/YwGEz7+GJz4+ml1UbWXBePyzXW1+N2hIGGn7BcNuA0v8rMY
uvVgiIIQNjLErgGcCWmMHLwsMMQ7LNprUZZKsSNB4HaQDH7cQZmYBN/O45np6l+K
VuLdzXdDpZcOM7bNO6smev822WPGDuKBo1iVfQbUe10X4dCNwkBR3QGpScVvg8gg
tRYZDYue/qc4Xaj806RZPttknWfxdvfZgoOmAiwnyQ5K3+mzNYHgQZAOC2ydkK4J
s+ZizK3lAgMBAAECggEAKwEcyXyrWmdLRQNcIDuSbD8ouzzSXIOp4BHQyH337nDQ
5nnY0PTns79VksU9TMktIS7PQZJF0brjOmmQU2SvcbAVG5y+mRmlMhwHhrPOuB4A
ahrWRrsQubV1+n/MRttJUEWS/WJmVuDp3NHAnI+VTYPkOHs4GeJXynik5PutjAr3
tYmr3kaw0Wo/hYAXTKsI/R5aenC7jH8ZSyVcZ/j+bOSH5sT5/JY122AYmkQOFE7s
JA0EfYJaJEwiuBWKOfRLQVEHhOFodUBZdGQcWeW3uFb88aYKN8QcKTO8/f6e4r8w
QojgK3QMj1zmfS7xid6XCOVa17ary2hZHAEPnjcigQKBgQDQnm4TlbVTsM+CbFUS
1rOIJRzPdnH3Y7x3IcmVKZt81eNktsdu56A4U6NEkFQqk4tVTT4TYja/hwgXmm6w
J+w0WwZd445Bxj8PmaEr6Z/NSMYbCsi8pRelKWmlIMwD2YhtY/1xXD37zpOgN8oQ
ryTKZR2gljbPxdfhKS7YerLp2wKBgQD/gJt3Ds69j1gMDLnnPctjmhsPRXh7PQ0e
E9lqgFkx/vNuCuyRs6ymic2rBZmkdlpjsTJFmz1bwOzIvSRoH6kp0Mfyo6why5kr
upDf7zz+hlvaFewme8aDeV3ex9Wvt73D66nwAy5ABOgn+66vZJeo0Iq/tnCwK3a/
evTL9BOzPwKBgEUi7AnziEc3Bl4Lttnqa08INZcPgs9grzmv6dVUF6J0Y8qhxFAd
1Pw1w5raVfpSMU/QrGzSFKC+iFECLgKVCHOFYwPEgQWNRKLP4BjkcMAgiP63QTU7
ZS2oHsnJp7Ly6YKPK5Pg5O3JVSU4t+91i7TDc+EfRwTuZQ/KjSrS5u4XAoGBAP06
v9reSDVELuWyb0Yqzrxm7k7ScbjjJ28aCTAvCTguEaKNHS7DP2jHx5mrMT35N1j7
NHIcjFG2AnhqTf0M9CJHlQR9B4tvON5ISHJJsNAq5jpd4/G4V2XTEiBNOxKvL1tQ
5NrGrD4zHs0R+25GarGcDwg3j7RrP4REHv9NZ4ENAoGAY7Nuz6xKu2XUwuZtJP7O
kjsoDS7bjP95ddrtsRq5vcVjJ04avnjsr+Se9WDA//t7+eSeHjm5eXD7u0NtdqZo
WtSm8pmWySOPXMn9QQmdzKHg1NOxer//f1KySVunX1vftTStjsZH7dRCtBEePcqg
z5Av6MmEFDojtwTqvEZuhBM=
-----END PRIVATE KEY-----`
	embeddedAppCRLPEM  = `-----BEGIN X509 CRL-----
MIIBbjBYAgEBMA0GCSqGSIb3DQEBCwUAMCYxJDAiBgNVBAMMG0dMT0YzODEzNzM0
MDg5LmJhbWJ1bGFiLmNvbRcNMjYxMDEwMDMyNjM1WhcNMjcxMDExMDMyNjM1WjAN
BgkqhkiG9w0BAQsFAAOCAQEAEnTkdg5VMAPvKm0m4CdyHZWIGMVnFEzb8jrN3K8C
xwlj/oo9Oc5QknE7HwhMkl/fIn3M+dvsJY/ysEIjNTzADZiGq7IpjUokui280MIn
aVqhtUtHLmuGrzho7JDF+Apt3GN5PaFsqlnWEtDJh5kHPwFhhzfadBW6FkhNLEcO
lkQabgmnpnkaA1SIdILMYbFhBSTpm8z1JOgDqW8Ujwrc/G548nv8tZy41aSSMuxP
O9V99Th9DAS233efK1lI7DyovaDeHLINdoALvbkQqRcNc/yJOx8j2EcZuFHXKIz9
JK88Yf+wDt561G5VLCXceDh/dU9833QzCjP/uvMa7flk6Q==
-----END X509 CRL-----`
)

const (
	// signedSequenceFloor is the lowest sequence_id the firmware accepts on a
	// signed frame. Below it the printer answers 84033545 even when the
	// certificate is trusted. It is a fixed floor, not a high-water mark.
	signedSequenceFloor = 2000

	// errCodeUnsigned is what the printer replies when a privileged command
	// arrives without a signature. Used to auto-detect printers that need one.
	errCodeUnsigned = 84033543
	// errCodeCertNotTrusted means the signature verified but the cert_id is
	// not in the printer's trust store, i.e. app_cert_install is needed or was
	// lost to a printer reboot.
	errCodeCertNotTrusted = 84033545
	// errCodeBadHeader means a required header field was missing or malformed.
	errCodeBadHeader = 84033546
	// errCodeSignatureRejected means the signature or payload_len did not
	// verify. A numeric payload_len lands here and is indistinguishable from a
	// bad certificate, which makes it an easy trap.
	errCodeSignatureRejected = 84033547
)

// commandSigner produces the signed wire form for print-namespace commands.
type commandSigner struct {
	key    *rsa.PrivateKey
	certID string
	userID string
	cert   string
	crl    string
	seq    atomic.Int64
}

// AppCertDir is where the signing credentials are cached so an operator can
// inspect or replace them without rebuilding the image. It defaults to the same
// directory as the token store, which deployments bind-mount to host storage.
var AppCertDir = DefaultTokenDir

const (
	appCertFileName = "bambu_app_cert.pem"
	appKeyFileName  = "bambu_app_key.pem"
	appCRLFileName  = "bambu_app_crl.pem"
)

// newCommandSigner loads the signing credentials from dir, seeding it with the
// embedded copies on first use. An empty dir means AppCertDir. Anything already
// on disk takes precedence, so replacing the files and restarting is enough to
// rotate credentials. If the directory cannot be written — read-only mount,
// wrong ownership — the embedded credentials are used instead and signing still
// works; a cache failure is never allowed to disable command signing.
func newCommandSigner(userID, dir string) (*commandSigner, error) {
	certPEM := []byte(embeddedAppCertPEM)
	keyPEM := []byte(embeddedAppKeyPEM)
	crlPEM := []byte(embeddedAppCRLPEM)

	if dir == "" {
		dir = AppCertDir
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Printf("bambu: cannot create cert dir %s, using embedded credentials: %v", dir, err)
		} else {
			certPEM = loadOrSeed(filepath.Join(dir, appCertFileName), certPEM)
			keyPEM = loadOrSeed(filepath.Join(dir, appKeyFileName), keyPEM)
			crlPEM = loadOrSeed(filepath.Join(dir, appCRLFileName), crlPEM)
		}
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("app key: no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse app key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("app key is %T, want *rsa.PrivateKey", key)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("app cert: no PEM block found")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse app cert: %w", err)
	}

	return &commandSigner{
		key:    rsaKey,
		certID: certIDFor(cert),
		userID: userID,
		cert:   string(certPEM),
		crl:    string(crlPEM),
	}, nil
}

// loadOrSeed returns the contents of path, writing def to it first when it is
// missing or empty. A write failure is logged and def is returned so a read-only
// or mis-owned data directory degrades to the embedded credentials.
func loadOrSeed(path string, def []byte) []byte {
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b
	}
	// 0600: the private key lands here. The cert and CRL are public but share
	// the mode rather than carrying three separate policies.
	if err := os.WriteFile(path, def, 0o600); err != nil {
		log.Printf("bambu: could not cache %s, using embedded copy: %v", filepath.Base(path), err)
		return def
	}
	log.Printf("bambu: cached %s", filepath.Base(path))
	return def
}

// certIDFor builds the identifier the printer matches a signature against:
// the lowercase hex of the certificate's serial number followed by "CN=" and
// the issuer common name. It is not a hash of the certificate.
func certIDFor(cert *x509.Certificate) string {
	hex := fmt.Sprintf("%x", cert.SerialNumber.Bytes())
	if len(hex)%2 == 1 {
		hex = "0" + hex
	}
	cn := cert.Issuer.CommonName
	if cn == "" {
		return hex
	}
	return hex + "CN=" + cn
}

// nextSequence returns a sequence id above the firmware's signed-frame floor.
func (s *commandSigner) nextSequence() string {
	n := s.seq.Add(1)
	return strconv.Itoa(signedSequenceFloor + int(n))
}

// sign wraps a {"print":{...}} payload in the header envelope and returns the
// exact bytes to publish. The signature covers the payload bytes with the
// header removed, so the returned slice must be published unmodified.
func (s *commandSigner) sign(payload []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(payload, &top); err != nil {
		return nil, fmt.Errorf("sign: payload not an object: %w", err)
	}
	raw, ok := top["print"]
	if !ok {
		return nil, fmt.Errorf(`sign: payload has no "print" section`)
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(raw, &inner); err != nil {
		return nil, fmt.Errorf("sign: print section not an object: %w", err)
	}
	if _, ok := inner["sequence_id"]; !ok {
		inner["sequence_id"] = json.RawMessage(strconv.Quote(s.nextSequence()))
	}
	printJSON, err := json.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("sign: re-marshal print: %w", err)
	}

	body := []byte(`{"print":` + string(printJSON) + `,"user_id":` + strconv.Quote(s.userID) + `}`)

	sum := sha256.Sum256(body)
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, sum[:])
	if err != nil {
		return nil, fmt.Errorf("sign: RSA sign: %w", err)
	}

	// payload_len MUST be a JSON string. A number here is rejected with
	// 84033547, which looks exactly like a certificate problem.
	header := fmt.Sprintf(
		`,"header":{"sign_ver":"v1.0","sign_alg":"RSA_SHA256","sign_string":"%s","cert_id":%s,"payload_len":"%d"}}`,
		base64.StdEncoding.EncodeToString(sig),
		strconv.Quote(s.certID),
		len(body),
	)

	// Splice rather than re-marshal so the signed bytes stay byte-identical to
	// what goes on the wire.
	out := make([]byte, 0, len(body)+len(header))
	out = append(out, body[:len(body)-1]...)
	out = append(out, header...)
	return out, nil
}

// certInstallPayload builds the security.app_cert_install command that puts our
// certificate into the printer's trust store. The command itself is not
// verified, which is what makes bootstrapping possible.
func (s *commandSigner) certInstallPayload() []byte {
	type securityPayload struct {
		SequenceID string `json:"sequence_id"`
		Command    string `json:"command"`
		AppCert    string `json:"app_cert"`
		CRL        string `json:"crl"`
	}
	b, err := json.Marshal(struct {
		Security securityPayload `json:"security"`
	}{
		Security: securityPayload{
			SequenceID: s.nextSequence(),
			Command:    "app_cert_install",
			AppCert:    s.cert,
			CRL:        s.crl,
		},
	})
	if err != nil {
		// Struct of strings; unreachable in practice.
		log.Printf("bambu: app_cert_install marshal failed: %v", err)
		return nil
	}
	return b
}
