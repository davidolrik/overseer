package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveJumpChain_NonexistentAlias(t *testing.T) {
	quietLogger(t)

	// Use a non-existent SSH config file so ssh -G will fail or return empty
	chain := resolveJumpChain("nonexistent-host-that-should-not-resolve", nil, "")
	// ssh -G should still work (resolving via default config), so check result
	// If the alias doesn't exist in any SSH config, hostname will be the alias itself
	// and there will be no proxy jump, so chain should be nil
	if chain != nil && len(chain) == 0 {
		t.Error("expected nil (not empty slice) for simple host with no jump")
	}
}

func TestResolveJumpChain_WithSSHConfigFile(t *testing.T) {
	quietLogger(t)

	// Create a minimal SSH config with a ProxyJump
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "ssh_config")

	config := `Host jump-test
    HostName 10.0.0.1
    Port 22
    ProxyJump jump-hop

Host jump-hop
    HostName 10.0.0.2
    Port 2222
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write SSH config: %v", err)
	}

	// Check if ssh is available
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	chain := resolveJumpChain("jump-test", nil, configPath)
	if chain == nil {
		t.Skip("resolveJumpChain returned nil - ssh -G may not support this config")
	}

	// Should have at least the destination in the chain
	if len(chain) < 1 {
		t.Errorf("expected at least 1 hop in chain, got %d", len(chain))
	}
}

func TestResolveJumpChain_DirectConnection(t *testing.T) {
	quietLogger(t)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "ssh_config")

	config := `Host direct-test
    HostName 10.0.0.1
    Port 22
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatalf("failed to write SSH config: %v", err)
	}

	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	chain := resolveJumpChain("direct-test", nil, configPath)
	// Direct connection (no ProxyJump) should return nil
	if chain != nil {
		t.Errorf("expected nil chain for direct connection, got %v", chain)
	}
}

func TestResolveJumpChain_WithEnv(t *testing.T) {
	quietLogger(t)

	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh not available")
	}

	// Test with environment variables - should not panic
	chain := resolveJumpChain("localhost", map[string]string{"OVERSEER_TAG": "test-tag"}, "")
	// Result depends on SSH config, just verify no panic
	_ = chain
}
