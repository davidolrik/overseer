package cmd

import (
	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

func NewDaemonCommand() *cobra.Command {
	daemonCmd := &cobra.Command{
		Use:     "daemon",
		Aliases: []string{"server"},
		Short:   "Run daemon in foreground (only for debugging)",
		Long: `Run daemon in foreground (only for debugging)

The daemon will be started automatically, so you rarely have to call this directly.

If you need to debug a connection, or just want to have the daemon running in the
foreground use this command.`,
		Run: func(cmd *cobra.Command, args []string) {
			d := daemon.New()
			d.Run()
		},
	}

	return daemonCmd
}
