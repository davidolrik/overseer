package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	BaseDirName = ".config/overseer"
	PidFileName = "daemon.pid"
	SocketName  = "daemon.sock"
)

var Config *viper.Viper

var globalFlagsToConfigKey = map[string]string{
	"config-path": "config_path",
	"verbose":     "verbose",
}

func GetSocketPath() string {
	return filepath.Join(Config.GetString("config_path"), SocketName)
}

func GetPIDFilePath() string {
	return filepath.Join(Config.GetString("config_path"), PidFileName)
}

func GetReconnectEnabled() bool {
	return Config.GetBool("reconnect.enabled")
}

func GetReconnectInitialBackoff() string {
	return Config.GetString("reconnect.initial_backoff")
}

func GetReconnectMaxBackoff() string {
	return Config.GetString("reconnect.max_backoff")
}

func GetReconnectBackoffFactor() int {
	return Config.GetInt("reconnect.backoff_factor")
}

func GetReconnectMaxRetries() int {
	return Config.GetInt("reconnect.max_retries")
}

func InitializeConfig(cmd *cobra.Command) ([]string, error) {
	Config = viper.New()

	// Set config path from user input
	configPath, err := cmd.Parent().Flags().GetString("config-path")
	if err != nil {
		panic("Unable to determine config path")
	}
	Config.AddConfigPath(configPath)

	// Set config name
	Config.SetConfigName("config")
	Config.SetConfigType("toml")

	// Set defaults
	Config.SetDefault("verbose", 0)
	Config.SetDefault("reconnect.enabled", true)
	Config.SetDefault("reconnect.initial_backoff", "1s")
	Config.SetDefault("reconnect.max_backoff", "5m")
	Config.SetDefault("reconnect.backoff_factor", 2)
	Config.SetDefault("reconnect.max_retries", 10)

	// Config.SetDefault("socket_path", 0)

	// Setup env reading
	Config.SetEnvPrefix("overseer")

	// Load config file
	if err := Config.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found - create config path and write config with defaults
			err := os.MkdirAll(configPath, 0o755)
			if err != nil {
				panic(err)
			}
			Config.SafeWriteConfig()
		} else {
			// Config file was found but another error occurred
			panic(err)
		}
	}

	// In order to get environment variables mapped into config sections, we need to replace . with _
	Config.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	Config.AutomaticEnv() // read in environment variables that match

	// Bind the current command's flags to viper
	if cmd != nil {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			// Is this a global flag
			configKey, ok := globalFlagsToConfigKey[f.Name]
			if !ok {
				return
			}

			// Apply the viper config value to the flag when the flag is not set and viper has a value
			if !f.Changed && Config.IsSet(configKey) {
				cmd.Flags().Set(f.Name, fmt.Sprintf("%v", Config.Get(configKey)))
			} else {
				Config.Set(configKey, fmt.Sprintf("%v", f.Value))
			}
		})
	}

	return []string{}, nil
}
