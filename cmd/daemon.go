package cmd

import (
	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewDaemonCommand() *cobra.Command {
	daemonCmd := &cobra.Command{
		Use:     "daemon",
		Aliases: []string{},
		Hidden:  true,
		Run: func(cmd *cobra.Command, args []string) {
			d := daemon.New()
			d.Run()
		},
	}

	return daemonCmd
}
