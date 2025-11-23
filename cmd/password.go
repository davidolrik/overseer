package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/keyring"
)

func NewPasswordCommand() *cobra.Command {
	passwordCmd := &cobra.Command{
		Use:     "password",
		Aliases: []string{"passwd", "pass"},
		Short:   "Manage stored passwords for SSH hosts",
		Long:    `Store, delete, and list passwords for SSH hosts. Passwords are stored securely in the system keyring.`,
	}

	// password set command
	setCmd := &cobra.Command{
		Use:               "set <alias>",
		Short:             "Store a password for an SSH host",
		Long:              `Store a password for an SSH host. The password is stored securely in the system keyring (Keychain on macOS, Secret Service on Linux).`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: sshHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]

			// Prompt for password with confirmation
			password, err := keyring.PromptAndConfirmPassword(alias)
			if err != nil {
				slog.Error(fmt.Sprintf("Failed to read password: %v", err))
				os.Exit(1)
			}

			// Store password in keyring
			if err := keyring.SetPassword(alias, password); err != nil {
				slog.Error(fmt.Sprintf("Failed to store password: %v", err))
				os.Exit(1)
			}

			slog.Info(fmt.Sprintf("Password stored securely for '%s'", alias))
		},
	}

	// password delete command
	deleteCmd := &cobra.Command{
		Use:               "delete <alias>",
		Aliases:           []string{"del", "remove", "rm"},
		Short:             "Delete a stored password for an SSH host",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: sshHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]

			if err := keyring.DeletePassword(alias); err != nil {
				slog.Error(fmt.Sprintf("Failed to delete password: %v", err))
				os.Exit(1)
			}

			slog.Info(fmt.Sprintf("Password deleted for '%s'", alias))
		},
	}

	// password list command
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List SSH hosts with stored passwords",
		Long:    `List SSH hosts that have passwords stored in the system keyring. Note: This command cannot list all stored passwords without checking each host alias.`,
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// Get all SSH host aliases
			homeDir, err := os.UserHomeDir()
			if err != nil {
				slog.Error(fmt.Sprintf("Failed to get home directory: %v", err))
				os.Exit(1)
			}
			sshConfigPath := filepath.Join(homeDir, ".ssh", "config")
			fullConfigString, err := recursivelyReadAllSSHConfigs(sshConfigPath, make(map[string]bool))
			if err != nil {
				slog.Error(fmt.Sprintf("Failed to read SSH config: %v", err))
				os.Exit(1)
			}

			hosts := extractHostAliases(fullConfigString)
			hostsWithPasswords := []string{}

			for _, host := range hosts {
				if keyring.HasPassword(host) {
					hostsWithPasswords = append(hostsWithPasswords, host)
				}
			}

			if len(hostsWithPasswords) == 0 {
				slog.Info("No stored passwords found")
				return
			}

			fmt.Println("SSH hosts with stored passwords:")
			for _, host := range hostsWithPasswords {
				fmt.Printf("  - %s\n", host)
			}
		},
	}

	passwordCmd.AddCommand(setCmd, deleteCmd, listCmd)
	return passwordCmd
}
