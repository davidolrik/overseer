package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"
	"go.olrik.dev/overseer/internal/core"
	"go.olrik.dev/overseer/internal/daemon"
)

func NewRootCommand() *cobra.Command {
	var configPath string
	var verbose int

	homeDir, _ := os.UserHomeDir()

	rootCmd := &cobra.Command{
		Use:   "overseer",
		Short: "Overseer - SSH Tunnel Manager",
		Long:  `Overseer - SSH Tunnel Manager`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("\033[1m\033[36mOverseer\033[0m \033[90m—\033[0m \033[37mSSH Tunnel Manager\033[0m")
			fmt.Println()

			daemon.CheckVersionMismatch()

			// Get tunnel status
			response, err := daemon.SendCommand("STATUS")
			if err != nil {
				slog.Warn("No active tunnels (daemon is not running).")
				fmt.Println()
				fmt.Println("Run 'overseer help' for available commands.")
				return nil
			}

			jsonBytes, _ := json.Marshal(response.Data)
			statuses := []daemon.DaemonStatus{}
			json.Unmarshal(jsonBytes, &statuses)

			// Sort tunnels by hostname for consistent output
			sort.Slice(statuses, func(i, j int) bool {
				return statuses[i].Hostname < statuses[j].Hostname
			})

			// Get context status with no events
			contextResponse, err := daemon.SendCommand("CONTEXT_STATUS 0")

			// Get companion status for tree display
			companionResponse, _ := daemon.SendCommand("COMPANION_STATUS")
			companionMap := getCompanionMap(companionResponse)

			// Show context banner and sensors
			if err == nil && contextResponse.Data != nil {
				displayContextBanner(contextResponse.Data)
				displayContextInfo(contextResponse.Data)
			}

			displayTunnels(statuses, companionMap)

			fmt.Println()
			fmt.Println("Run 'overseer help' for available commands.")

			return nil
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Initialize config and bind global flags to the config
			messages, err := core.InitializeConfig(cmd)
			for _, message := range messages {
				fmt.Println(message)
			}
			if err != nil {
				return err
			}

			// Set global logger with custom options
			w := os.Stderr
			slog.SetDefault(slog.New(
				tint.NewHandler(w, &tint.Options{
					Level:      slog.LevelDebug,
					TimeFormat: time.DateTime,
				}),
			))

			return nil
		},
	}
	rootCmd.PersistentFlags().StringVar(
		&configPath, "config-path", fmt.Sprintf("%s/%s", homeDir, core.BaseDirName),
		"config path",
	)
	rootCmd.PersistentFlags().CountVarP(&verbose, "verbose", "v", "more output, repeat for even more")

	rootCmd.AddCommand(
		NewAskpassCommand(),
		NewAttachCommand(),
		NewBackfillCommand(),
		NewCompanionCommand(),
		NewCompanionRunCommand(),
		NewConnectCommand(),
		NewDaemonCommand(),
		NewDisconnectCommand(),
		NewLogsCommand(),
		NewPasswordCommand(),
		NewReconnectCommand(),
		NewReloadCommand(),
		NewResetCommand(),
		NewRestartCommand(),
		NewStartCommand(),
		NewStatsCommand(),
		NewStatusCommand(),
		NewStopCommand(),
		NewVersionCommand(),
	)

	return rootCmd
}
