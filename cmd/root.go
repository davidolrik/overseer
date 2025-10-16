package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/cobra"
	"olrik.dev/davidolrik/overseer/internal/core"
)

func NewRootCommand() *cobra.Command {
	var configPath string
	var verbose int

	homeDir, _ := os.UserHomeDir()

	rootCmd := &cobra.Command{
		Use:   "overseer",
		Short: "Overseer - SSH Tunnel Manager",
		Long:  `Overseer - SSH Tunnel Manager`,
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
		NewDaemonCommand(),
		NewPasswordCommand(),
		NewQuitCommand(),
		NewResetCommand(),
		NewStartCommand(),
		NewStatusCommand(),
		NewStopCommand(),
		NewVersionCommand(),
	)

	return rootCmd
}
