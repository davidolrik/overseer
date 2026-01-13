package cmd

import (
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewConnectCommand() *cobra.Command {
	var tags []string

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

			// Build command with optional tags
			command := "SSH_CONNECT " + alias
			if len(tags) > 0 {
				command += " --tags=" + strings.Join(tags, ",")
			}

			// Use streaming to show companion startup progress in real-time
			if err := daemon.SendCommandStreaming(command); err != nil {
				slog.Error(err.Error())
				os.Exit(1)
			}
		},
	}

	connectCmd.Flags().StringArrayVarP(&tags, "tag", "T", []string{},
		"SSH tag for config matching (can be specified multiple times)")

	return connectCmd
}
