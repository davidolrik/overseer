package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewAskpassCommand() *cobra.Command {
	askpassCmd := &cobra.Command{
		Use:    "askpass",
		Short:  "Internal SSH askpass helper (do not call directly)",
		Long:   `Internal command used by SSH_ASKPASS mechanism. Do not call this directly.`,
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// Get alias and token from environment variables
			alias := os.Getenv("OVERSEER_ASKPASS_ALIAS")
			token := os.Getenv("OVERSEER_ASKPASS_TOKEN")

			if alias == "" || token == "" {
				// Not called correctly
				os.Exit(1)
			}

			// Ask daemon for password, daemon will validate token
			response, err := daemon.SendCommand(fmt.Sprintf("ASKPASS %s %s", alias, token))
			if err != nil {
				// Daemon not running or validation failed
				os.Exit(1)
			}

			// Check if we got a password back
			if len(response.Messages) == 0 || response.Messages[0].Status != "INFO" {
				os.Exit(1)
			}

			// Output password to stdout (SSH will read this)
			// The password is in the first message
			fmt.Println(response.Messages[0].Message)
		},
	}

	return askpassCmd
}
