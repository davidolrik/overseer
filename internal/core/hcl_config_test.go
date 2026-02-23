package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// loadTestConfig is a helper that writes HCL to a temp file and loads it
func loadTestConfig(t *testing.T, hcl string) (*Configuration, error) {
	t.Helper()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.hcl")
	if err := os.WriteFile(configPath, []byte(hcl), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}
	return LoadConfig(configPath)
}

func TestLoadConfig_SSHDefaults(t *testing.T) {
	t.Run("no SSH block uses defaults", func(t *testing.T) {
		config, err := loadTestConfig(t, `verbose = 0`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if config.SSH.ServerAliveInterval != 15 {
			t.Errorf("expected ServerAliveInterval=15, got %d", config.SSH.ServerAliveInterval)
		}
		if config.SSH.ServerAliveCountMax != 3 {
			t.Errorf("expected ServerAliveCountMax=3, got %d", config.SSH.ServerAliveCountMax)
		}
		if !config.SSH.ReconnectEnabled {
			t.Error("expected ReconnectEnabled=true by default")
		}
		if config.SSH.InitialBackoff != "1s" {
			t.Errorf("expected InitialBackoff='1s', got %q", config.SSH.InitialBackoff)
		}
		if config.SSH.MaxBackoff != "5m" {
			t.Errorf("expected MaxBackoff='5m', got %q", config.SSH.MaxBackoff)
		}
		if config.SSH.BackoffFactor != 2 {
			t.Errorf("expected BackoffFactor=2, got %d", config.SSH.BackoffFactor)
		}
		if config.SSH.MaxRetries != 10 {
			t.Errorf("expected MaxRetries=10, got %d", config.SSH.MaxRetries)
		}
	})

	t.Run("empty SSH block uses defaults for zero values", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0
ssh {}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if config.SSH.ServerAliveInterval != 15 {
			t.Errorf("expected ServerAliveInterval=15, got %d", config.SSH.ServerAliveInterval)
		}
		if !config.SSH.ReconnectEnabled {
			t.Error("expected ReconnectEnabled=true when not explicitly set")
		}
	})

	t.Run("reconnect_enabled can be set to false", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0
ssh {
  reconnect_enabled = false
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if config.SSH.ReconnectEnabled {
			t.Error("expected ReconnectEnabled=false")
		}
	})
}

func TestLoadConfig_Hooks(t *testing.T) {
	t.Run("context hooks", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

context "home" {
  display_name = "Home"

  conditions {
    public_ip = ["1.2.3.4"]
  }

  hooks {
    on_enter = ["echo entering home"]
    on_leave = ["echo leaving home"]
    timeout  = "10s"
  }

  actions {
    connect = ["vpn"]
  }
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		ctx := config.Contexts[0]
		if ctx.Hooks == nil {
			t.Fatal("expected hooks to be parsed")
		}
		if len(ctx.Hooks.OnEnter) != 1 {
			t.Fatalf("expected 1 on_enter hook, got %d", len(ctx.Hooks.OnEnter))
		}
		if ctx.Hooks.OnEnter[0].Command != "echo entering home" {
			t.Errorf("expected command='echo entering home', got %q", ctx.Hooks.OnEnter[0].Command)
		}
		if ctx.Hooks.OnEnter[0].Timeout != 10*time.Second {
			t.Errorf("expected timeout=10s, got %v", ctx.Hooks.OnEnter[0].Timeout)
		}
		if len(ctx.Hooks.OnLeave) != 1 {
			t.Fatalf("expected 1 on_leave hook, got %d", len(ctx.Hooks.OnLeave))
		}
		if ctx.Hooks.OnLeave[0].Command != "echo leaving home" {
			t.Errorf("expected command='echo leaving home', got %q", ctx.Hooks.OnLeave[0].Command)
		}
	})

	t.Run("location hooks", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

location "office" {
  display_name = "Office"

  conditions {
    public_ip = ["10.0.0.0/8"]
  }

  hooks {
    on_enter = ["notify-send 'At office'"]
  }
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		loc := config.Locations["office"]
		if loc.Hooks == nil {
			t.Fatal("expected hooks to be parsed")
		}
		if len(loc.Hooks.OnEnter) != 1 {
			t.Fatalf("expected 1 on_enter hook, got %d", len(loc.Hooks.OnEnter))
		}
		if loc.Hooks.OnEnter[0].Command != "notify-send 'At office'" {
			t.Errorf("expected command='notify-send 'At office'', got %q", loc.Hooks.OnEnter[0].Command)
		}
		// Default timeout is 30s when not specified
		if loc.Hooks.OnEnter[0].Timeout != 30*time.Second {
			t.Errorf("expected default timeout=30s, got %v", loc.Hooks.OnEnter[0].Timeout)
		}
	})

	t.Run("global hooks", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

context_hooks {
  on_enter = ["echo context changed"]
  on_leave = ["echo context leaving"]
}

location_hooks {
  on_enter = ["echo location changed"]
  timeout  = "5s"
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if config.GlobalContextHooks == nil {
			t.Fatal("expected global context hooks")
		}
		if len(config.GlobalContextHooks.OnEnter) != 1 {
			t.Errorf("expected 1 global context on_enter hook, got %d", len(config.GlobalContextHooks.OnEnter))
		}
		if len(config.GlobalContextHooks.OnLeave) != 1 {
			t.Errorf("expected 1 global context on_leave hook, got %d", len(config.GlobalContextHooks.OnLeave))
		}

		if config.GlobalLocationHooks == nil {
			t.Fatal("expected global location hooks")
		}
		if len(config.GlobalLocationHooks.OnEnter) != 1 {
			t.Errorf("expected 1 global location on_enter hook, got %d", len(config.GlobalLocationHooks.OnEnter))
		}
		if config.GlobalLocationHooks.OnEnter[0].Timeout != 5*time.Second {
			t.Errorf("expected timeout=5s, got %v", config.GlobalLocationHooks.OnEnter[0].Timeout)
		}
	})

	t.Run("tunnel hooks", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  hooks {
    before_connect {
      command = "echo before"
      timeout = "15s"
    }
    after_connect {
      command = "echo after"
    }
  }
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		tun := config.Tunnels["vpn"]
		if tun.Hooks == nil {
			t.Fatal("expected tunnel hooks")
		}
		if len(tun.Hooks.BeforeConnect) != 1 {
			t.Fatalf("expected 1 before_connect hook, got %d", len(tun.Hooks.BeforeConnect))
		}
		if tun.Hooks.BeforeConnect[0].Command != "echo before" {
			t.Errorf("expected command='echo before', got %q", tun.Hooks.BeforeConnect[0].Command)
		}
		if tun.Hooks.BeforeConnect[0].Timeout != 15*time.Second {
			t.Errorf("expected timeout=15s, got %v", tun.Hooks.BeforeConnect[0].Timeout)
		}
		if len(tun.Hooks.AfterConnect) != 1 {
			t.Fatalf("expected 1 after_connect hook, got %d", len(tun.Hooks.AfterConnect))
		}
		// Default timeout for tunnel hook
		if tun.Hooks.AfterConnect[0].Timeout != 30*time.Second {
			t.Errorf("expected default timeout=30s, got %v", tun.Hooks.AfterConnect[0].Timeout)
		}
	})
}

func TestLoadConfig_Companions(t *testing.T) {
	t.Run("basic companion", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "setup" {
    command   = "echo hello"
    wait_mode = "completion"
    timeout   = "10s"
  }
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		tun := config.Tunnels["vpn"]
		if len(tun.Companions) != 1 {
			t.Fatalf("expected 1 companion, got %d", len(tun.Companions))
		}

		comp := tun.Companions[0]
		if comp.Name != "setup" {
			t.Errorf("expected name='setup', got %q", comp.Name)
		}
		if comp.Command != "echo hello" {
			t.Errorf("expected command='echo hello', got %q", comp.Command)
		}
		if comp.WaitMode != "completion" {
			t.Errorf("expected wait_mode='completion', got %q", comp.WaitMode)
		}
		if comp.Timeout != 10*time.Second {
			t.Errorf("expected timeout=10s, got %v", comp.Timeout)
		}
		if comp.OnFailure != "block" {
			t.Errorf("expected on_failure='block' (default), got %q", comp.OnFailure)
		}
		if !comp.KeepAlive {
			t.Error("expected keep_alive=true (default)")
		}
		if comp.AutoRestart {
			t.Error("expected auto_restart=false (default)")
		}
		if comp.Persistent {
			t.Error("expected persistent=false (default)")
		}
		if comp.StopSignal != "INT" {
			t.Errorf("expected stop_signal='INT' (default), got %q", comp.StopSignal)
		}
	})

	t.Run("companion with string wait mode", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "auth" {
    command   = "kinit"
    wait_mode = "string"
    wait_for  = "authenticated"
    timeout   = "30s"
  }
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		comp := config.Tunnels["vpn"].Companions[0]
		if comp.WaitMode != "string" {
			t.Errorf("expected wait_mode='string', got %q", comp.WaitMode)
		}
		if comp.WaitFor != "authenticated" {
			t.Errorf("expected wait_for='authenticated', got %q", comp.WaitFor)
		}
	})

	t.Run("companion with all options", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "sidecar" {
    command      = "/usr/local/bin/proxy"
    workdir      = "/tmp"
    wait_mode    = "completion"
    on_failure   = "continue"
    keep_alive   = false
    auto_restart = true
    persistent   = true
    stop_signal  = "TERM"
    ready_delay  = "2s"
    environment  = {
      "LOG_LEVEL" = "debug"
    }
  }
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		comp := config.Tunnels["vpn"].Companions[0]
		if comp.Workdir != "/tmp" {
			t.Errorf("expected workdir='/tmp', got %q", comp.Workdir)
		}
		if comp.OnFailure != "continue" {
			t.Errorf("expected on_failure='continue', got %q", comp.OnFailure)
		}
		if comp.KeepAlive {
			t.Error("expected keep_alive=false")
		}
		if !comp.AutoRestart {
			t.Error("expected auto_restart=true")
		}
		if !comp.Persistent {
			t.Error("expected persistent=true")
		}
		if comp.StopSignal != "TERM" {
			t.Errorf("expected stop_signal='TERM', got %q", comp.StopSignal)
		}
		if comp.ReadyDelay != 2*time.Second {
			t.Errorf("expected ready_delay=2s, got %v", comp.ReadyDelay)
		}
		if comp.Environment["LOG_LEVEL"] != "debug" {
			t.Errorf("expected LOG_LEVEL='debug', got %q", comp.Environment["LOG_LEVEL"])
		}
	})
}

func TestLoadConfig_CompanionValidationErrors(t *testing.T) {
	t.Run("duplicate companion name", func(t *testing.T) {
		_, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "dup" {
    command = "echo one"
  }
  companion "dup" {
    command = "echo two"
  }
}
`)
		if err == nil {
			t.Fatal("expected error for duplicate companion name")
		}
		if !strings.Contains(err.Error(), "duplicate companion name") {
			t.Errorf("expected 'duplicate companion name' error, got: %v", err)
		}
	})

	t.Run("missing command", func(t *testing.T) {
		_, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "empty" {
    command = ""
  }
}
`)
		if err == nil {
			t.Fatal("expected error for missing command")
		}
		if !strings.Contains(err.Error(), "command is required") {
			t.Errorf("expected 'command is required' error, got: %v", err)
		}
	})

	t.Run("invalid wait_mode", func(t *testing.T) {
		_, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "bad" {
    command   = "echo hello"
    wait_mode = "invalid"
  }
}
`)
		if err == nil {
			t.Fatal("expected error for invalid wait_mode")
		}
		if !strings.Contains(err.Error(), "wait_mode must be") {
			t.Errorf("expected 'wait_mode must be' error, got: %v", err)
		}
	})

	t.Run("string wait_mode without wait_for", func(t *testing.T) {
		_, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "bad" {
    command   = "echo hello"
    wait_mode = "string"
  }
}
`)
		if err == nil {
			t.Fatal("expected error for missing wait_for")
		}
		if !strings.Contains(err.Error(), "wait_for is required") {
			t.Errorf("expected 'wait_for is required' error, got: %v", err)
		}
	})

	t.Run("invalid on_failure", func(t *testing.T) {
		_, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "bad" {
    command    = "echo hello"
    on_failure = "explode"
  }
}
`)
		if err == nil {
			t.Fatal("expected error for invalid on_failure")
		}
		if !strings.Contains(err.Error(), "on_failure must be") {
			t.Errorf("expected 'on_failure must be' error, got: %v", err)
		}
	})

	t.Run("invalid timeout duration", func(t *testing.T) {
		_, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "bad" {
    command = "echo hello"
    timeout = "not-a-duration"
  }
}
`)
		if err == nil {
			t.Fatal("expected error for invalid timeout")
		}
		if !strings.Contains(err.Error(), "invalid timeout") {
			t.Errorf("expected 'invalid timeout' error, got: %v", err)
		}
	})

	t.Run("invalid ready_delay duration", func(t *testing.T) {
		_, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  companion "bad" {
    command     = "echo hello"
    ready_delay = "not-a-duration"
  }
}
`)
		if err == nil {
			t.Fatal("expected error for invalid ready_delay")
		}
		if !strings.Contains(err.Error(), "invalid ready_delay") {
			t.Errorf("expected 'invalid ready_delay' error, got: %v", err)
		}
	})
}

func TestLoadConfig_NestedConditions(t *testing.T) {
	t.Run("any block inside context", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

context "multi" {
  display_name = "Multi Location"

  conditions {
    any {
      public_ip = ["1.2.3.4"]
    }
    any {
      public_ip = ["5.6.7.8"]
    }
  }

  actions {
    connect = ["vpn"]
  }
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		ctx := config.Contexts[0]
		if ctx.Condition == nil {
			t.Fatal("expected condition to be parsed")
		}
		condStr := fmt.Sprintf("%v", ctx.Condition)
		if !strings.Contains(condStr, "1.2.3.4") {
			t.Errorf("expected condition to contain '1.2.3.4', got %s", condStr)
		}
		if !strings.Contains(condStr, "5.6.7.8") {
			t.Errorf("expected condition to contain '5.6.7.8', got %s", condStr)
		}
	})

	t.Run("online boolean condition", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

context "offline" {
  display_name = "Offline"

  conditions {
    online = false
  }

  actions {
    disconnect = ["all"]
  }
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		ctx := config.Contexts[0]
		if ctx.Condition == nil {
			t.Fatal("expected condition to be parsed")
		}
		condStr := fmt.Sprintf("%v", ctx.Condition)
		if !strings.Contains(condStr, "online") {
			t.Errorf("expected condition to contain 'online', got %s", condStr)
		}
	})
}

func TestLoadConfig_Exports(t *testing.T) {
	t.Run("all export types", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

exports {
  dotenv    = "/tmp/overseer.env"
  context   = "/tmp/context.txt"
  location  = "/tmp/location.txt"
  public_ip = "/tmp/ip.txt"
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if len(config.Exports) != 4 {
			t.Fatalf("expected 4 exports, got %d", len(config.Exports))
		}

		exportsByType := make(map[string]string)
		for _, e := range config.Exports {
			exportsByType[e.Type] = e.Path
		}

		expected := map[string]string{
			"dotenv":    "/tmp/overseer.env",
			"context":   "/tmp/context.txt",
			"location":  "/tmp/location.txt",
			"public_ip": "/tmp/ip.txt",
		}

		for typ, path := range expected {
			if exportsByType[typ] != path {
				t.Errorf("expected %s export path=%q, got %q", typ, path, exportsByType[typ])
			}
		}
	})

	t.Run("preferred_ip ipv6", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

exports {
  preferred_ip = "ipv6"
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if config.PreferredIP != "ipv6" {
			t.Errorf("expected preferred_ip='ipv6', got %q", config.PreferredIP)
		}
	})

	t.Run("preferred_ip defaults to ipv4", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

exports {}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if config.PreferredIP != "ipv4" {
			t.Errorf("expected preferred_ip='ipv4' (default), got %q", config.PreferredIP)
		}
	})
}

func TestLoadConfig_CompanionSettings(t *testing.T) {
	t.Run("custom history size", func(t *testing.T) {
		config, err := loadTestConfig(t, `
verbose = 0

companion {
  history_size = 500
}
`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if config.Companion.HistorySize != 500 {
			t.Errorf("expected history_size=500, got %d", config.Companion.HistorySize)
		}
	})

	t.Run("default history size", func(t *testing.T) {
		config, err := loadTestConfig(t, `verbose = 0`)
		if err != nil {
			t.Fatalf("Failed to load: %v", err)
		}

		if config.Companion.HistorySize != 1000 {
			t.Errorf("expected default history_size=1000, got %d", config.Companion.HistorySize)
		}
	})
}

func TestLoadConfig_TunnelTag(t *testing.T) {
	config, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  tag = "corp-vpn"
}
`)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	tun := config.Tunnels["vpn"]
	if tun.Tag != "corp-vpn" {
		t.Errorf("expected tag='corp-vpn', got %q", tun.Tag)
	}
}

func TestLoadConfig_InvalidHCL(t *testing.T) {
	_, err := loadTestConfig(t, `this is not valid HCL {{{`)
	if err == nil {
		t.Fatal("expected error for invalid HCL")
	}
}

func TestLoadConfig_InvalidHookTimeout(t *testing.T) {
	_, err := loadTestConfig(t, `
verbose = 0

context_hooks {
  on_enter = ["echo hi"]
  timeout  = "not-valid"
}
`)
	if err == nil {
		t.Fatal("expected error for invalid hook timeout")
	}
}

func TestLoadConfig_InvalidTunnelHookTimeout(t *testing.T) {
	_, err := loadTestConfig(t, `
verbose = 0

tunnel "vpn" {
  hooks {
    before_connect {
      command = "echo hi"
      timeout = "bad"
    }
  }
}
`)
	if err == nil {
		t.Fatal("expected error for invalid tunnel hook timeout")
	}
}

func TestGetDefaultConfig(t *testing.T) {
	config := GetDefaultConfig()

	if config == nil {
		t.Fatal("expected non-nil config")
	}
	if !config.CheckOnStartup {
		t.Error("expected CheckOnStartup=true")
	}
	if !config.CheckOnNetworkChange {
		t.Error("expected CheckOnNetworkChange=true")
	}
	if config.Verbose != 0 {
		t.Errorf("expected Verbose=0, got %d", config.Verbose)
	}
	if config.Locations == nil {
		t.Error("expected Locations map to be initialized")
	}
	if config.Contexts == nil {
		t.Error("expected Contexts slice to be initialized")
	}
	if config.Tunnels == nil {
		t.Error("expected Tunnels map to be initialized")
	}
	if config.SSH.ReconnectEnabled != true {
		t.Error("expected SSH.ReconnectEnabled=true")
	}
	if config.Companion.HistorySize != 1000 {
		t.Errorf("expected Companion.HistorySize=1000, got %d", config.Companion.HistorySize)
	}
}

func TestConfigExists(t *testing.T) {
	t.Run("existing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "config.hcl")
		os.WriteFile(path, []byte("verbose = 0"), 0644)

		if !ConfigExists(path) {
			t.Error("expected ConfigExists to return true for existing file")
		}
	})

	t.Run("non-existing file", func(t *testing.T) {
		if ConfigExists("/nonexistent/path/config.hcl") {
			t.Error("expected ConfigExists to return false for non-existing file")
		}
	})
}

func TestLoadConfig_EmptyConditionsReturnNilCondition(t *testing.T) {
	config, err := loadTestConfig(t, `
verbose = 0

context "fallback" {
  display_name = "Fallback"

  actions {
    disconnect = ["all"]
  }
}
`)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	ctx := config.Contexts[0]
	if ctx.Condition != nil {
		t.Errorf("expected nil condition for context without conditions block, got %v", ctx.Condition)
	}
}

func TestLoadConfig_EnvironmentDefaultsToEmptyMap(t *testing.T) {
	config, err := loadTestConfig(t, `
verbose = 0

location "test" {
  display_name = "Test"
  conditions {
    public_ip = ["1.2.3.4"]
  }
}

context "test" {
  display_name = "Test"
  actions {
    connect = ["vpn"]
  }
}
`)
	if err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	loc := config.Locations["test"]
	if loc.Environment == nil {
		t.Error("expected location Environment to be initialized (not nil)")
	}

	ctx := config.Contexts[0]
	if ctx.Environment == nil {
		t.Error("expected context Environment to be initialized (not nil)")
	}
}
