package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.hcl")

	// Write a test HCL config
	hclConfig := `# Test configuration
verbose = 0

exports {
  context  = "/tmp/test-context.txt"
  dotenv   = "/tmp/overseer.env"
}

ssh {
  server_alive_interval = 15
  server_alive_count_max = 3
  reconnect_enabled = true
  initial_backoff = "1s"
  max_backoff = "5m"
  backoff_factor = 2
  max_retries = 10
}

context "home" {
  display_name = "Home"

  conditions {
    public_ip = ["123.45.67.89", "123.45.67.90", "192.168.1.0/24"]
  }

  actions {
    connect    = ["homelab", "nas"]
    disconnect = ["office-vpn"]
  }
}

context "office" {
  display_name = "Office"

  conditions {
    public_ip = ["98.76.54.0/24", "98.76.55.0/24"]
  }

  actions {
    connect    = ["office-vpn"]
    disconnect = ["homelab"]
  }
}
`

	err := os.WriteFile(configPath, []byte(hclConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load the configuration
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load HCL config: %v", err)
	}

	// Verify basic settings
	if config.Verbose != 0 {
		t.Errorf("Expected verbose=0, got %v", config.Verbose)
	}

	// Verify exports
	if len(config.Exports) != 2 {
		t.Fatalf("Expected 2 exports, got %d", len(config.Exports))
	}

	// Find context export
	var contextExport, dotenvExport *ExportConfig
	for i := range config.Exports {
		if config.Exports[i].Type == "context" {
			contextExport = &config.Exports[i]
		} else if config.Exports[i].Type == "dotenv" {
			dotenvExport = &config.Exports[i]
		}
	}

	if contextExport == nil {
		t.Error("Expected to find context export")
	} else if contextExport.Path != "/tmp/test-context.txt" {
		t.Errorf("Expected context export path='/tmp/test-context.txt', got '%v'", contextExport.Path)
	}

	if dotenvExport == nil {
		t.Error("Expected to find dotenv export")
	} else if dotenvExport.Path != "/tmp/overseer.env" {
		t.Errorf("Expected dotenv export path='/tmp/overseer.env', got '%v'", dotenvExport.Path)
	}

	// Verify SSH settings (including reconnect)
	if config.SSH.ServerAliveInterval != 15 {
		t.Errorf("Expected ssh.server_alive_interval=15, got %v", config.SSH.ServerAliveInterval)
	}

	if config.SSH.ServerAliveCountMax != 3 {
		t.Errorf("Expected ssh.server_alive_count_max=3, got %v", config.SSH.ServerAliveCountMax)
	}

	if !config.SSH.ReconnectEnabled {
		t.Error("Expected ssh.reconnect_enabled=true")
	}

	if config.SSH.InitialBackoff != "1s" {
		t.Errorf("Expected ssh.initial_backoff='1s', got '%v'", config.SSH.InitialBackoff)
	}

	if config.SSH.MaxBackoff != "5m" {
		t.Errorf("Expected ssh.max_backoff='5m', got '%v'", config.SSH.MaxBackoff)
	}

	if config.SSH.BackoffFactor != 2 {
		t.Errorf("Expected ssh.backoff_factor=2, got %v", config.SSH.BackoffFactor)
	}

	if config.SSH.MaxRetries != 10 {
		t.Errorf("Expected ssh.max_retries=10, got %v", config.SSH.MaxRetries)
	}

	// Verify context rules
	if len(config.Contexts) != 2 {
		t.Fatalf("Expected 2 context rules, got %d", len(config.Contexts))
	}

	// Check home context (should be first in order)
	homeRule := config.Contexts[0]
	if homeRule.Name != "home" {
		t.Errorf("Expected first context name='home', got '%v'", homeRule.Name)
	}

	if homeRule.DisplayName != "Home" {
		t.Errorf("Expected display_name='Home', got '%v'", homeRule.DisplayName)
	}

	// Check that conditions were parsed
	if homeRule.Condition == nil {
		t.Fatal("Expected conditions to be parsed")
	}

	// Verify the structured condition was created correctly
	condStr := fmt.Sprintf("%v", homeRule.Condition)
	t.Logf("Parsed condition: %s", condStr)

	// Should contain all three IP patterns
	expectedPatterns := []string{"123.45.67.89", "123.45.67.90", "192.168.1.0/24"}
	for _, pattern := range expectedPatterns {
		if !strings.Contains(condStr, pattern) {
			t.Errorf("Expected condition to contain pattern '%s'", pattern)
		}
	}

	// Check actions
	expectedConnects := []string{"homelab", "nas"}
	if len(homeRule.Actions.Connect) != len(expectedConnects) {
		t.Fatalf("Expected %d connect actions, got %d", len(expectedConnects), len(homeRule.Actions.Connect))
	}

	for i, conn := range expectedConnects {
		if homeRule.Actions.Connect[i] != conn {
			t.Errorf("Expected connect[%d]='%s', got '%s'", i, conn, homeRule.Actions.Connect[i])
		}
	}

	expectedDisconnects := []string{"office-vpn"}
	if len(homeRule.Actions.Disconnect) != len(expectedDisconnects) {
		t.Fatalf("Expected %d disconnect actions, got %d", len(expectedDisconnects), len(homeRule.Actions.Disconnect))
	}

	// Check office context
	officeRule := config.Contexts[1]
	if officeRule.Name != "office" {
		t.Errorf("Expected second context name='office', got '%v'", officeRule.Name)
	}

	if officeRule.DisplayName != "Office" {
		t.Errorf("Expected display_name='Office', got '%v'", officeRule.DisplayName)
	}

	t.Logf("HCL config loaded successfully")
	t.Logf("  Verbose: %v", config.Verbose)
	t.Logf("  SSH reconnect enabled: %v", config.SSH.ReconnectEnabled)
	t.Logf("  Context rules: %d", len(config.Contexts))
}

func TestLoadConfig_StructuredConditions(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.hcl")

	// Write a test HCL config with structured conditions
	hclConfig := `# Test configuration with structured conditions
verbose = 0

context "trusted" {
  display_name = "Trusted Location"

  conditions {
    online    = true
    public_ip = ["192.168.1.0/24"]
  }

  actions {
    connect = ["homelab"]
  }
}

context "offline" {
  display_name = "Offline Mode"

  conditions {
    online = false
  }

  actions {
    disconnect = ["all-tunnels"]
  }
}
`

	err := os.WriteFile(configPath, []byte(hclConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load the configuration
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load HCL config: %v", err)
	}

	// Verify contexts
	if len(config.Contexts) != 2 {
		t.Fatalf("Expected 2 context rules, got %d", len(config.Contexts))
	}

	// Find trusted context
	trustedRule := config.Contexts[0]
	if trustedRule.Name != "trusted" {
		t.Errorf("Expected first context name='trusted', got '%v'", trustedRule.Name)
	}

	if trustedRule.DisplayName != "Trusted Location" {
		t.Errorf("Expected display_name='Trusted Location', got '%v'", trustedRule.DisplayName)
	}

	// Check that structured condition was parsed
	if trustedRule.Condition == nil {
		t.Error("Expected structured condition for trusted, got nil")
	} else {
		condStr := fmt.Sprintf("%v", trustedRule.Condition)
		t.Logf("Trusted condition: %s", condStr)

		// Should contain both online and public_ip conditions
		if !strings.Contains(condStr, "online") {
			t.Error("Expected condition to contain 'online'")
		}
		if !strings.Contains(condStr, "192.168.1.0/24") {
			t.Error("Expected condition to contain IP pattern")
		}
	}

	// Find offline context
	offlineRule := config.Contexts[1]
	if offlineRule.Name != "offline" {
		t.Errorf("Expected second context name='offline', got '%v'", offlineRule.Name)
	}

	if offlineRule.Condition == nil {
		t.Error("Expected structured condition for offline, got nil")
	} else {
		condStr := fmt.Sprintf("%v", offlineRule.Condition)
		t.Logf("Offline condition: %s", condStr)

		// Should be a boolean condition for online=false
		if !strings.Contains(condStr, "online") {
			t.Error("Expected condition to contain 'online'")
		}
	}

	t.Logf("Structured conditions loaded successfully")
	t.Logf("  Contexts: %d", len(config.Contexts))
}

func TestLoadConfig_LocationsAndEnvironment(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.hcl")

	// Write a test HCL config with locations and environment variables
	hclConfig := `# Test configuration with locations
verbose = 0

location "home" {
  display_name = "Home Office"

  conditions {
    public_ip = ["192.168.1.0/24"]
  }

  environment = {
    "LOCATION_TYPE" = "home"
    "NETWORK"       = "trusted"
  }
}

location "office" {
  display_name = "Work Office"

  conditions {
    public_ip = ["10.0.0.0/8"]
    env = {
      "CORP_NETWORK" = "true"
    }
  }
}

context "work" {
  display_name = "Work Mode"
  locations    = ["home", "office"]

  actions {
    connect = ["vpn"]
  }

  environment = {
    "MODE" = "work"
  }
}
`

	err := os.WriteFile(configPath, []byte(hclConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load the configuration
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load HCL config: %v", err)
	}

	// Verify locations
	if len(config.Locations) != 2 {
		t.Fatalf("Expected 2 locations, got %d", len(config.Locations))
	}

	// Check home location
	homeLoc, ok := config.Locations["home"]
	if !ok {
		t.Fatal("Expected to find 'home' location")
	}

	if homeLoc.DisplayName != "Home Office" {
		t.Errorf("Expected display_name='Home Office', got '%v'", homeLoc.DisplayName)
	}

	if len(homeLoc.Environment) != 2 {
		t.Errorf("Expected 2 environment variables, got %d", len(homeLoc.Environment))
	}

	if homeLoc.Environment["LOCATION_TYPE"] != "home" {
		t.Errorf("Expected LOCATION_TYPE='home', got '%v'", homeLoc.Environment["LOCATION_TYPE"])
	}

	// Check office location with env condition
	officeLoc, ok := config.Locations["office"]
	if !ok {
		t.Fatal("Expected to find 'office' location")
	}

	if officeLoc.Condition == nil {
		t.Error("Expected structured condition for office location")
	} else {
		condStr := fmt.Sprintf("%v", officeLoc.Condition)
		t.Logf("Office location condition: %s", condStr)
	}

	// Check work context with locations
	if len(config.Contexts) != 1 {
		t.Fatalf("Expected 1 context, got %d", len(config.Contexts))
	}

	workCtx := config.Contexts[0]
	if len(workCtx.Locations) != 2 {
		t.Errorf("Expected 2 locations in work context, got %d", len(workCtx.Locations))
	}

	if workCtx.Environment["MODE"] != "work" {
		t.Errorf("Expected MODE='work', got '%v'", workCtx.Environment["MODE"])
	}

	t.Logf("Locations and environment loaded successfully")
}
