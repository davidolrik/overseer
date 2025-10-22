package cmd

import (
	"encoding/json"
	"log/slog"
	"os"

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

	jsonBytes, _ := json.Marshal(response.Data)
	statuses := []daemon.DaemonStatus{}
	json.Unmarshal(jsonBytes, &statuses)

	var activeHosts []string
	for _, status := range statuses {
		activeHosts = append(activeHosts, status.Hostname)
	}

	return activeHosts, cobra.ShellCompDirectiveNoFileComp
}

func NewDisconnectCommand() *cobra.Command {
	disconnectCmd := &cobra.Command{
		Use:               "disconnect <alias>",
		Aliases:           []string{"d"},
		Short:             "Disconnect SSH tunnel",
		Long:              `Disconnect SSH tunnel`,
		Args:              cobra.RangeArgs(0, 1),
		ValidArgsFunction: activeHostCompletionFunc,
		Run: func(cmd *cobra.Command, args []string) {
			daemon.CheckVersionMismatch()
			if len(args) == 1 {
				alias := args[0]
				response, err := daemon.SendCommand("SSH_DISCONNECT " + alias)
				if err != nil {
					// This typically means the daemon wasn't running in the first place.
					slog.Error("Could not connect to daemon. Nothing to disconnect.")
					os.Exit(1)
				}
				response.LogMessages()
			} else {
				response, err := daemon.SendCommand("SSH_DISCONNECT_ALL")
				if err != nil {
					// This typically means the daemon wasn't running in the first place.
					slog.Error("Could not connect to daemon. Nothing to disconnect.")
					os.Exit(1)

				}
				response.LogMessages()
			}
		},
	}

	return disconnectCmd
}
