package bambu

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func testSigner(t *testing.T) *commandSigner {
	t.Helper()
	s, err := newCommandSigner("1578637590", t.TempDir())
	if err != nil {
		t.Fatalf("newCommandSigner: %v", err)
	}
	return s
}

// The printer identifies a signature by the certificate's serial number in
// lowercase hex plus "CN=" and the issuer common name. It is NOT a hash of the
// certificate; getting this wrong yields err_code 84033545.
func TestCertIDForIsSerialPlusIssuerCN(t *testing.T) {
	s := testSigner(t)
	block, _ := pem.Decode([]byte(embeddedAppCertPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := certIDFor(cert)
	want := "ee3cac0712ceeacda8b7fbc42e74ba2cCN=GLOF3813734089.bambulab.com"
	if got != want {
		t.Errorf("certIDFor = %q, want %q", got, want)
	}
	if s.certID != want {
		t.Errorf("signer.certID = %q, want %q", s.certID, want)
	}
}

// The signature must cover the exact bytes the printer sees with "header"
// removed. This is the invariant that broke for hours: a numeric payload_len,
// or any re-marshal between signing and publishing, yields err_code 84033547
// with no other symptom.
func TestSignProducesVerifiableEnvelope(t *testing.T) {
	s := testSigner(t)
	block, _ := pem.Decode([]byte(embeddedAppCertPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pub := cert.PublicKey.(*rsa.PublicKey)

	tests := []struct {
		name    string
		payload string
	}{
		{"gcode_line", `{"print":{"command":"gcode_line","param":"G28\n"}}`},
		{"pause no sequence", `{"print":{"command":"pause"}}`},
		{"existing sequence preserved", `{"print":{"command":"skip","sequence_id":"900001"}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := s.sign([]byte(tc.payload))
			if err != nil {
				t.Fatalf("sign: %v", err)
			}

			var env map[string]json.RawMessage
			if err := json.Unmarshal(out, &env); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, out)
			}
			hdrRaw, ok := env["header"]
			if !ok {
				t.Fatalf("no header in %s", out)
			}
			var hdr struct {
				SignVer    string          `json:"sign_ver"`
				SignAlg    string          `json:"sign_alg"`
				SignString string          `json:"sign_string"`
				CertID     string          `json:"cert_id"`
				PayloadLen json.RawMessage `json:"payload_len"`
			}
			if err := json.Unmarshal(hdrRaw, &hdr); err != nil {
				t.Fatalf("header parse: %v", err)
			}

			if hdr.SignVer != "v1.0" || hdr.SignAlg != "RSA_SHA256" {
				t.Errorf("sign_ver/alg = %q/%q, want v1.0/RSA_SHA256", hdr.SignVer, hdr.SignAlg)
			}
			if hdr.CertID != s.certID {
				t.Errorf("cert_id = %q, want %q", hdr.CertID, s.certID)
			}
			// MUST be a JSON string. A number here is the 84033547 trap.
			if !strings.HasPrefix(string(hdr.PayloadLen), `"`) {
				t.Errorf("payload_len must be a JSON string, got %s", hdr.PayloadLen)
			}

			sig, err := base64.StdEncoding.DecodeString(hdr.SignString)
			if err != nil {
				t.Fatalf("sign_string not base64: %v", err)
			}

			// Rebuild the signed bytes the way the printer does: the envelope
			// with "header" removed, byte-identical to what we published.
			body := stripHeader(out)

			var declaredLenStr string
			if err := json.Unmarshal(hdr.PayloadLen, &declaredLenStr); err != nil {
				t.Fatalf("payload_len must unmarshal as a string, got %s: %v", hdr.PayloadLen, err)
			}
			declaredLen, err := strconv.Atoi(declaredLenStr)
			if err != nil {
				t.Fatalf("payload_len %q not numeric: %v", declaredLenStr, err)
			}
			if declaredLen != len(body) {
				t.Errorf("payload_len = %d, actual signed bytes = %d", declaredLen, len(body))
			}

			sum := sha256.Sum256(body)
			if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
				t.Errorf("signature does not verify over stripped bytes: %v\nbody=%s", err, body)
			}
		})
	}
}

// stripHeader removes the "header" member from the serialized envelope and
// returns the remaining bytes, mimicking what the firmware verifies against.
func stripHeader(out []byte) []byte {
	s := string(out)
	i := strings.LastIndex(s, `,"header":`)
	if i < 0 {
		return out
	}
	return []byte(s[:i] + "}")
}

// The firmware requires sequence_id >= 2000 on signed frames; below that it
// answers 84033545 even when the certificate is trusted.
func TestSignInjectsSequenceAboveFloor(t *testing.T) {
	s := testSigner(t)
	out, err := s.sign([]byte(`{"print":{"command":"pause"}}`))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	var env struct {
		Print struct {
			SequenceID string `json:"sequence_id"`
			Command    string `json:"command"`
		} `json:"print"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Print.Command != "pause" {
		t.Errorf("command = %q, want pause", env.Print.Command)
	}
	var n int
	if _, err := fmt.Sscan(env.Print.SequenceID, &n); err != nil {
		t.Fatalf("sequence_id %q not numeric: %v", env.Print.SequenceID, err)
	}
	if n < signedSequenceFloor {
		t.Errorf("sequence_id %d below floor %d", n, signedSequenceFloor)
	}

	// An explicit sequence_id must survive untouched.
	out2, err := s.sign([]byte(`{"print":{"command":"x","sequence_id":"900001"}}`))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(string(out2), `"sequence_id":"900001"`) {
		t.Errorf("existing sequence_id was replaced: %s", out2)
	}
}

func TestSignRejectsNonPrintPayload(t *testing.T) {
	s := testSigner(t)
	if _, err := s.sign([]byte(`{"system":{"command":"ledctrl"}}`)); err == nil {
		t.Error("expected error for system-namespace payload, got nil")
	}
	if _, err := s.sign([]byte(`not json`)); err == nil {
		t.Error("expected error for malformed payload, got nil")
	}
}

// app_cert_install is how the printer learns to trust our cert. The crl field
// must be non-empty (the printer answers "crl value is null" otherwise) even
// though its signature is not validated.
func TestCertInstallPayloadShape(t *testing.T) {
	s := testSigner(t)
	b := s.certInstallPayload()
	if b == nil {
		t.Fatal("nil payload")
	}
	var p struct {
		Security struct {
			SequenceID string `json:"sequence_id"`
			Command    string `json:"command"`
			AppCert    string `json:"app_cert"`
			CRL        string `json:"crl"`
		} `json:"security"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Security.Command != "app_cert_install" {
		t.Errorf("command = %q", p.Security.Command)
	}
	if !strings.Contains(p.Security.AppCert, "BEGIN CERTIFICATE") {
		t.Error("app_cert missing PEM")
	}
	if p.Security.CRL == "" {
		t.Error("crl must be non-empty or the printer rejects the install")
	}
	if !strings.Contains(p.Security.CRL, "BEGIN X509 CRL") {
		t.Error("crl missing PEM")
	}
}

func TestIsPrintPayload(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{`{"print":{"command":"pause"}}`, true},
		{`{"system":{"command":"ledctrl"}}`, false},
		{`{"pushing":{"command":"pushall"}}`, false},
		{`garbage`, false},
		{`[]`, false},
	}
	for _, tc := range tests {
		if got := isPrintPayload([]byte(tc.in)); got != tc.want {
			t.Errorf("isPrintPayload(%s) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRejectedCodeExtraction(t *testing.T) {
	code := 84033543
	ack := commandACK{Command: "gcode_line", Result: "failed", Reason: "mqtt message verify failed", ErrCode: &code}
	err := ack.failed("gcode_line")
	if got := rejectedCode(err); got != 84033543 {
		t.Errorf("rejectedCode = %d, want 84033543", got)
	}
	// Message text must stay stable; other tests and the UI depend on it.
	if msg := err.Error(); !strings.Contains(msg, "printer rejected gcode_line") || !strings.Contains(msg, "84033543") {
		t.Errorf("unexpected message: %s", msg)
	}
	if got := rejectedCode(nil); got != 0 {
		t.Errorf("rejectedCode(nil) = %d, want 0", got)
	}
}

// The credentials are cached into the data directory (bind-mounted in
// deployment) so an operator can inspect or replace them without rebuilding.
func TestSignerSeedsCertFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := newCommandSigner("1578637590", dir); err != nil {
		t.Fatalf("newCommandSigner: %v", err)
	}
	for name, want := range map[string]string{
		appCertFileName: embeddedAppCertPEM,
		appKeyFileName:  embeddedAppKeyPEM,
		appCRLFileName:  embeddedAppCRLPEM,
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s not cached: %v", name, err)
		}
		if string(b) != want {
			t.Errorf("%s content does not match the embedded copy", name)
		}
	}
	// The private key lands here, so it must not be world-readable.
	fi, err := os.Stat(filepath.Join(dir, appKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("%s mode = %v, want no group/other bits", appKeyFileName, fi.Mode().Perm())
	}
}

// Whatever is on disk wins, which is what makes rotation possible without a
// rebuild. Writing a non-key where the key belongs proves the file is read.
func TestSignerPrefersDiskOverEmbedded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, appKeyFileName), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newCommandSigner("1578637590", dir); err == nil {
		t.Fatal("expected the on-disk (invalid) key to be used instead of the embedded one")
	}
}

// A data directory that cannot be written must degrade to the embedded
// credentials rather than disabling command signing.
func TestSignerFallsBackWhenCacheUnwritable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// MkdirAll through a regular file fails for any uid.
	s, err := newCommandSigner("1578637590", filepath.Join(blocker, "sub"))
	if err != nil {
		t.Fatalf("unwritable cache dir must not be fatal, got: %v", err)
	}
	if s.certID == "" {
		t.Error("signer did not fall back to the embedded credentials")
	}
	if !strings.Contains(s.cert, "BEGIN CERTIFICATE") {
		t.Error("embedded cert not used in fallback")
	}
}

// A second call must reuse the cached files rather than overwrite them.
func TestSignerDoesNotOverwriteExistingCache(t *testing.T) {
	dir := t.TempDir()
	if _, err := newCommandSigner("1578637590", dir); err != nil {
		t.Fatal(err)
	}
	marker := []byte("operator-supplied\n")
	if err := os.WriteFile(filepath.Join(dir, appCRLFileName), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := newCommandSigner("1578637590", dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.crl != string(marker) {
		t.Errorf("cached CRL was overwritten: %q", s.crl)
	}
}
