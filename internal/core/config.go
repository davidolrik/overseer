package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	BaseDirName = ".config/overseer"
	PidFileName = "daemon.pid"
	SocketName  = "daemon.sock"
)

// GetSocketPath returns the path to the daemon socket
func GetSocketPath() string {
	return filepath.Join(Config.ConfigPath, SocketName)
}

// GetPIDFilePath returns the path to the daemon PID file
func GetPIDFilePath() string {
	return filepath.Join(Config.ConfigPath, PidFileName)
}

// InitializeConfig loads the configuration from the KDL file
func InitializeConfig(cmd *cobra.Command) ([]string, error) {
	// Get config path from user input
	configPath, err := cmd.Parent().Flags().GetString("config-path")
	if err != nil {
		panic("Unable to determine config path")
	}

	// Load KDL config
	kdlPath := filepath.Join(configPath, "config.kdl")
	if _, err := os.Stat(kdlPath); err == nil {
		// KDL file exists, parse it
		Config, err = LoadConfig(kdlPath)
		if err != nil {
			// Clean up the error message
			errMsg := err.Error()
			errMsg = strings.TrimPrefix(errMsg, "failed to unmarshal KDL: parse failed: ")
			errMsg = strings.TrimPrefix(errMsg, "failed to unmarshal KDL: scan failed: ")

			// Remove the visual pointer (everything after the line/column info)
			if idx := strings.Index(errMsg, ":\n"); idx != -1 {
				errMsg = errMsg[:idx]
			}

			fmt.Fprintf(os.Stderr, "Error: Configuration file has syntax errors\n")
			fmt.Fprintf(os.Stderr, "  File: %s\n", kdlPath)
			fmt.Fprintf(os.Stderr, "  %s\n", errMsg)
			os.Exit(1)
		}
	} else {
		// No config file found - create default KDL config
		err := os.MkdirAll(configPath, 0o755)
		if err != nil {
			panic(err)
		}
		// Write default KDL config
		if err := writeDefaultKDLConfig(kdlPath); err != nil {
			panic(fmt.Sprintf("Failed to write default config: %v", err))
		}
		// Load the newly created config
		Config, err = LoadConfig(kdlPath)
		if err != nil {
			// This should never happen with default config, but handle it gracefully
			fmt.Fprintf(os.Stderr, "Error: Failed to parse default configuration: %v\n", err)
			os.Exit(1)
		}
	}

	// Set the config path
	Config.ConfigPath = configPath

	// Override verbose from command-line flag if provided
	if cmd != nil {
		if verboseFlag, err := cmd.Flags().GetCount("verbose"); err == nil && verboseFlag > 0 {
			Config.Verbose = verboseFlag
		}
	}

	return []string{}, nil
}

// writeDefaultKDLConfig writes a default KDL configuration file
func writeDefaultKDLConfig(path string) error {
	defaultConfig := `// Overseer Configuration
// See https://kdl.dev for KDL syntax reference

// Global settings
verbose 0

// Reconnect settings for SSH tunnels
reconnect {
  enabled true
  initial_backoff "1s"
  max_backoff "5m"
  backoff_factor 2
  max_retries 10
}

// Example context: Uncomment and customize for your network
// Contexts are evaluated in order (first match wins), so put more specific contexts first
// context "home" {
//   display_name "Home"
//
//   conditions {
//     public_ip "92.0.2.42"
//   }
//
//   actions {
//     connect "home-lab"
//   }
// }
`
	return os.WriteFile(path, []byte(defaultConfig), 0644)
}
