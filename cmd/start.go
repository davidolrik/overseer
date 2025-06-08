package cmd

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

func NewStartCommand() *cobra.Command {
	startCmd := &cobra.Command{
		Use:     "start",
		Aliases: []string{},
		Short:   "Start tunnel",
		Long:    `Start tunnel`,
		Args:    cobra.ExactArgs(1),
		ValidArgsFunction: sshHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]
			daemon.EnsureDaemonIsRunning()
			response, err := daemon.SendCommand("START " + alias)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Print(response)
		},
	}

	return startCmd
}
