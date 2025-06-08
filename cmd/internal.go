package cmd

import (
	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewInternalCommand() *cobra.Command {
	internalCmd := &cobra.Command{
		Use:     "internal-server",
		Aliases: []string{},
		Hidden:  true,
		Run: func(cmd *cobra.Command, args []string) {
			d := daemon.New()
			d.Run()
		},
	}

	return internalCmd
}
