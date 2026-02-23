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

// InitializeConfig loads the configuration from the HCL file
func InitializeConfig(cmd *cobra.Command) ([]string, error) {
	// Get config path from user input
	configDir, err := cmd.Parent().Flags().GetString("config-path")
	if err != nil {
		panic("Unable to determine config path")
	}

	// Load HCL config
	hclPath := filepath.Join(configDir, "config.hcl")
	configDPath := filepath.Join(configDir, "config.d")
	if _, err := os.Stat(hclPath); err == nil {
		// HCL file exists, parse it (along with any config.d/ fragments)
		Config, err = LoadConfigDir(hclPath, configDPath)
		if err != nil {
			// Clean up the error message
			errMsg := err.Error()
			errMsg = strings.TrimPrefix(errMsg, "failed to parse HCL config: ")

			// Remove the visual pointer (everything after the line/column info)
			if idx := strings.Index(errMsg, ":\n"); idx != -1 {
				errMsg = errMsg[:idx]
			}

			fmt.Fprintf(os.Stderr, "Error: Configuration has errors\n")
			fmt.Fprintf(os.Stderr, "  %s\n", errMsg)
			os.Exit(1)
		}
	} else {
		// No config file found - create default HCL config
		err := os.MkdirAll(configDir, 0o755)
		if err != nil {
			panic(err)
		}
		// Write default HCL config
		if err := writeDefaultHCLConfig(hclPath); err != nil {
			panic(fmt.Sprintf("Failed to write default config: %v", err))
		}
		// Load the newly created config
		Config, err = LoadConfigDir(hclPath, configDPath)
		if err != nil {
			// This should never happen with default config, but handle it gracefully
			fmt.Fprintf(os.Stderr, "Error: Failed to parse default configuration: %v\n", err)
			os.Exit(1)
		}
	}

	// Set the config path
	Config.ConfigPath = configDir

	// Override verbose from command-line flag if provided
	if cmd != nil {
		if verboseFlag, err := cmd.Flags().GetCount("verbose"); err == nil && verboseFlag > 0 {
			Config.Verbose = verboseFlag
		}
	}

	return []string{}, nil
}

// writeDefaultHCLConfig writes a default HCL configuration file
func writeDefaultHCLConfig(path string) error {
	defaultConfig := `# Overseer Configuration

# Global settings
verbose = 0

# Optional: Export context data to files for external integration
# All export paths support ~ for home directory
# exports {
#   dotenv    = "/path/to/overseer.env"      # Env file with OVERSEER_* variables
#   context   = "/path/to/context.txt"       # Context name only
#   location  = "/path/to/location.txt"      # Location name only
#   public_ip = "/path/to/public_ip.txt"     # Public IP only
# }

# SSH connection settings
ssh {
  # Keep alive settings - detect dead connections
  server_alive_interval = 15  # Send keepalive every N seconds (0 to disable)
  server_alive_count_max = 3  # Exit after N failed keepalives

  # Automatic reconnection settings
  reconnect_enabled = true
  initial_backoff   = "1s"    # First retry delay
  max_backoff       = "5m"    # Maximum delay between retries
  backoff_factor    = 2       # Multiplier for each retry
  max_retries       = 10      # Give up after this many attempts
}

# Location definitions - reusable network/physical locations
# Uncomment and customize for your networks
# location "home" {
#   display_name = "Home Network"
#
#   conditions {
#     public_ip = ["203.0.113.42", "198.51.100.0/24"]
#     env = {
#       "HOSTNAME" = "my-laptop"
#     }
#   }
#
#   environment = {
#     "LOCATION_TYPE"  = "residential"
#     "NETWORK_SPEED"  = "1000"
#   }
# }

# Context definitions - evaluated in order (first match wins)
# Contexts can reference locations or use direct conditions
# context "trusted" {
#   display_name = "Trusted Network"
#   locations    = ["home", "office"]
#
#   environment = {
#     "TRUST_LEVEL" = "high"
#   }
#
#   actions {
#     connect = ["home-lab", "dev-server"]
#   }
# }
#
# context "mobile" {
#   display_name = "Mobile Network"
#
#   conditions {
#     public_ip = ["109.58.0.0/16"]
#   }
#
#   environment = {
#     "TRUST_LEVEL"  = "low"
#     "REQUIRE_VPN"  = "true"
#   }
#
#   actions {
#     connect    = ["vpn"]
#     disconnect = ["home-lab"]
#   }
# }
`
	return os.WriteFile(path, []byte(defaultConfig), 0644)
}
