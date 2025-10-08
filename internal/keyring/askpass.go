package keyring

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
)

// generateToken generates a random authentication token
func generateToken() (string, error) {
	bytes := make([]byte, 32) // 256 bits
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// ConfigureSSHAskpass configures an SSH command to use the overseer binary as askpass helper
// Returns the generated token that must be stored in the daemon for validation
func ConfigureSSHAskpass(cmd *exec.Cmd, alias string) (string, error) {
	// Get the path to the current overseer binary
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	// Generate authentication token to prevent unauthorized askpass calls
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("failed to generate auth token: %w", err)
	}

	// Set SSH_ASKPASS to just the executable path
	// The askpass command will communicate with daemon via socket
	cmd.Env = append(cmd.Env, fmt.Sprintf("SSH_ASKPASS=%s", execPath))

	// Pass the alias via environment variable
	cmd.Env = append(cmd.Env, fmt.Sprintf("OVERSEER_ASKPASS_ALIAS=%s", alias))

	// Set the authentication token that daemon will validate
	cmd.Env = append(cmd.Env, fmt.Sprintf("OVERSEER_ASKPASS_TOKEN=%s", token))

	// For OpenSSH 8.4+, use SSH_ASKPASS_REQUIRE=force
	cmd.Env = append(cmd.Env, "SSH_ASKPASS_REQUIRE=force")

	// For older OpenSSH versions, set DISPLAY to trigger askpass behavior
	cmd.Env = append(cmd.Env, "DISPLAY=:0")

	// Detach from terminal by setting stdin to /dev/null
	cmd.Stdin = nil

	return token, nil
}
