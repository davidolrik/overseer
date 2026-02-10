package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewConnectCommand() *cobra.Command {
	var tag string

	connectCmd := &cobra.Command{
		Use:               "connect",
		Aliases:           []string{"c"},
		Short:             "Connect SSH tunnel",
		Long:              `Connect SSH tunnel`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: sshHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]
			daemon.EnsureDaemonIsRunning()
			daemon.CheckVersionMismatch()

			// Build command with optional tag
			command := "SSH_CONNECT " + alias
			if tag != "" {
				command += " --tag=" + tag
			}

			// Use streaming to show companion startup progress in real-time
			if err := daemon.SendCommandStreaming(command); err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}
		},
	}

	connectCmd.Flags().StringVarP(&tag, "tag", "T", "",
		"SSH tag for config matching (passed as -P to ssh for Match tagged)")

	return connectCmd
}
