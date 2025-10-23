package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create temporary directory
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.kdl")

	// Write a test KDL config
	kdlConfig := `// Test configuration
verbose 0
context_output_file "/tmp/test-context.txt"

reconnect {
  enabled true
  initial_backoff "1s"
  max_backoff "5m"
  backoff_factor 2
  max_retries 10
}

context "home" {
  display_name "Home"

  conditions {
    public_ip "123.45.67.89"
    public_ip "123.45.67.90"
    public_ip "192.168.1.0/24"
  }

  actions {
    connect "homelab"
    connect "nas"
    disconnect "office-vpn"
  }
}

context "office" {
  display_name "Office"

  conditions {
    public_ip "98.76.54.0/24"
    public_ip "98.76.55.0/24"
  }

  actions {
    connect "office-vpn"
    disconnect "homelab"
  }
}
`

	err := os.WriteFile(configPath, []byte(kdlConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load the configuration
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load KDL config: %v", err)
	}

	// Verify basic settings
	if config.Verbose != 0 {
		t.Errorf("Expected verbose=0, got %v", config.Verbose)
	}

	if config.ContextOutputFile != "/tmp/test-context.txt" {
		t.Errorf("Expected output_file='/tmp/test-context.txt', got '%v'", config.ContextOutputFile)
	}

	// Verify reconnect settings
	if !config.Reconnect.Enabled {
		t.Error("Expected reconnect.enabled=true")
	}

	if config.Reconnect.InitialBackoff != "1s" {
		t.Errorf("Expected reconnect.initial_backoff='1s', got '%v'", config.Reconnect.InitialBackoff)
	}

	if config.Reconnect.MaxBackoff != "5m" {
		t.Errorf("Expected reconnect.max_backoff='5m', got '%v'", config.Reconnect.MaxBackoff)
	}

	if config.Reconnect.BackoffFactor != 2 {
		t.Errorf("Expected reconnect.backoff_factor=2, got %v", config.Reconnect.BackoffFactor)
	}

	if config.Reconnect.MaxRetries != 10 {
		t.Errorf("Expected reconnect.max_retries=10, got %v", config.Reconnect.MaxRetries)
	}

	// Verify context rules
	if len(config.Contexts) != 2 {
		t.Fatalf("Expected 2 context rules, got %d", len(config.Contexts))
	}

	// Check home context
	homeRule, ok := config.Contexts["home"]
	if !ok {
		t.Fatal("Could not find home context rule")
	}

	if homeRule.Name != "home" {
		t.Errorf("Expected name='home', got '%v'", homeRule.Name)
	}

	if homeRule.DisplayName != "Home" {
		t.Errorf("Expected display_name='Home', got '%v'", homeRule.DisplayName)
	}

	// Check conditions
	publicIPs, ok := homeRule.Conditions["public_ip"]
	if !ok {
		t.Fatal("Expected public_ip condition")
	}

	expectedIPs := []string{"123.45.67.89", "123.45.67.90", "192.168.1.0/24"}
	if len(publicIPs) != len(expectedIPs) {
		t.Fatalf("Expected %d public IPs, got %d", len(expectedIPs), len(publicIPs))
	}

	for i, ip := range expectedIPs {
		if publicIPs[i] != ip {
			t.Errorf("Expected public_ip[%d]='%s', got '%s'", i, ip, publicIPs[i])
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
	officeRule, ok := config.Contexts["office"]
	if !ok {
		t.Fatal("Could not find office context rule")
	}

	if officeRule.DisplayName != "Office" {
		t.Errorf("Expected display_name='Office', got '%v'", officeRule.DisplayName)
	}

	t.Logf("✓ KDL config loaded successfully")
	t.Logf("  Verbose: %v", config.Verbose)
	t.Logf("  Reconnect enabled: %v", config.Reconnect.Enabled)
	t.Logf("  Context rules: %d", len(config.Contexts))
	t.Logf("  Home context IPs: %v", publicIPs)
}
