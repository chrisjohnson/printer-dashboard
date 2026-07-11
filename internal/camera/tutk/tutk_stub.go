//go:build !(linux && amd64)

package tutk

import (
	"context"
	"errors"
)

var ErrUnsupportedPlatform = errors.New("TUTK P2P camera requires linux/amd64 platform with libBambuSource.so")

// Session manages the lifecycle of a single TUTK P2P camera connection.
// On non-linux/amd64 platforms, all methods return ErrUnsupportedPlatform.
type Session struct {
	creds Credentials
}

// NewSession creates a new TUTK session from credentials.
func NewSession(creds Credentials) (*Session, error) {
	return nil, ErrUnsupportedPlatform
}

// Open connects to the camera via TUTK P2P. Never succeeds on stub.
func (s *Session) Open(ctx context.Context) error {
	return ErrUnsupportedPlatform
}

// ReadSample reads the next JPEG frame. Never succeeds on stub.
func (s *Session) ReadSample() ([]byte, error) {
	return nil, ErrUnsupportedPlatform
}

// Close tears down the TUTK connection. No-op on stub.
func (s *Session) Close() error {
	return nil
}
