package cmd

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/daemon"
)

// activeHostCompletionFunc connects to the daemon, gets the status,
// and returns a list of currently active tunnel aliases.
func activeHostCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Send the STATUS command to the daemon.
	response, err := daemon.SendCommand("STATUS")
	if err != nil {
		// If the daemon isn't running, there are no active hosts to stop.
		// Return an empty list.
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var activeHosts []string
	lines := strings.Split(response, "\n")

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		// Active host lines in our status output start with a hyphen.
		// Example line: "  - my-server (PID: 12345)"
		if strings.HasPrefix(trimmedLine, "-") {
			fields := strings.Fields(trimmedLine)
			if len(fields) >= 2 {
				// The alias is the second "word" on the line.
				activeHosts = append(activeHosts, fields[1])
			}
		}
	}

	return activeHosts, cobra.ShellCompDirectiveNoFileComp
}

func NewStopCommand() *cobra.Command {
	stopCmd := &cobra.Command{
		Use:               "stop <alias>",
		Aliases:           []string{},
		Short:             "Stop tunnel",
		Long:              `Stop tunnel`,
		Args:              cobra.RangeArgs(0, 1),
		ValidArgsFunction: activeHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 1 {
				alias := args[0]
				response, err := daemon.SendCommand("STOP " + alias)
				if err != nil {
					// This typically means the daemon wasn't running in the first place.
					log.Fatal("Could not connect to daemon. Nothing to stop.")
				}
				fmt.Print(response)
			} else {
				response, err := daemon.SendCommand("STOPALL")
				if err != nil {
					// This typically means the daemon wasn't running in the first place.
					log.Fatal("Could not connect to daemon. Nothing to stop.")
				}
				fmt.Print(response)
			}
		},
	}

	return stopCmd
}
