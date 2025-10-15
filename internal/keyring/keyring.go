package keyring

import (
	"fmt"
	"sync"

	"github.com/99designs/keyring"
)

const (
	serviceName = "overseer-ssh"
)

var (
	ring     keyring.Keyring
	ringOnce sync.Once
	ringErr  error
)

// initKeyring initializes the keyring with fallback options
func initKeyring() (keyring.Keyring, error) {
	ringOnce.Do(func() {
		// On macOS, prioritize Keychain and don't include FileBackend fallback
		// to avoid the "No directory provided" error
		ring, ringErr = keyring.Open(keyring.Config{
			ServiceName: serviceName,
			// Allow multiple backends with priority order
			AllowedBackends: []keyring.BackendType{
				keyring.KeychainBackend,      // macOS Keychain
				keyring.SecretServiceBackend, // Linux Secret Service (GNOME Keyring, KWallet)
				keyring.WinCredBackend,       // Windows Credential Manager
				keyring.PassBackend,          // Pass (password-store.org)
			},
		})
	})
	return ring, ringErr
}

// SetPassword stores a password for the given SSH host alias
func SetPassword(alias, password string) error {
	kr, err := initKeyring()
	if err != nil {
		return fmt.Errorf("failed to open keyring: %w", err)
	}

	return kr.Set(keyring.Item{
		Key:  alias,
		Data: []byte(password),
	})
}

// GetPassword retrieves a password for the given SSH host alias
// Returns empty string if no password is stored
func GetPassword(alias string) (string, error) {
	kr, err := initKeyring()
	if err != nil {
		return "", fmt.Errorf("failed to open keyring: %w", err)
	}

	item, err := kr.Get(alias)
	if err == keyring.ErrKeyNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to retrieve password: %w", err)
	}
	return string(item.Data), nil
}

// DeletePassword removes a stored password for the given SSH host alias
func DeletePassword(alias string) error {
	kr, err := initKeyring()
	if err != nil {
		return fmt.Errorf("failed to open keyring: %w", err)
	}

	err = kr.Remove(alias)
	if err == keyring.ErrKeyNotFound {
		return fmt.Errorf("no password stored for '%s'", alias)
	}
	return err
}

// HasPassword checks if a password is stored for the given alias
func HasPassword(alias string) bool {
	kr, err := initKeyring()
	if err != nil {
		return false
	}

	_, err = kr.Get(alias)
	return err == nil
}
