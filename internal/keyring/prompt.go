package keyring

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// PromptPassword prompts the user to enter a password securely (no echo)
func PromptPassword(alias string) (string, error) {
	fmt.Fprintf(os.Stderr, "Enter password for '%s': ", alias)

	// Try to open /dev/tty directly for terminal input
	// Fall back to stdin if tty is not available
	fd := int(os.Stdin.Fd())
	tty, err := os.Open("/dev/tty")
	if err == nil {
		defer tty.Close()
		fd = int(tty.Fd())
	}

	passwordBytes, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr) // Print newline after password input

	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}

	return string(passwordBytes), nil
}

// PromptAndConfirmPassword prompts for a password twice and confirms they match
func PromptAndConfirmPassword(alias string) (string, error) {
	password1, err := PromptPassword(alias)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "Confirm password for '%s': ", alias)

	// Try to open /dev/tty directly for terminal input
	// Fall back to stdin if tty is not available
	fd := int(os.Stdin.Fd())
	tty, err := os.Open("/dev/tty")
	if err == nil {
		defer tty.Close()
		fd = int(tty.Fd())
	}

	passwordBytes, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)

	if err != nil {
		return "", fmt.Errorf("failed to read password confirmation: %w", err)
	}

	password2 := string(passwordBytes)

	if password1 != password2 {
		return "", fmt.Errorf("passwords do not match")
	}

	return password1, nil
}
