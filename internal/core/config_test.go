package core

import (
	"path/filepath"
	"testing"
)

func TestGetSocketPath(t *testing.T) {
	// Save and restore Config
	original := Config
	defer func() { Config = original }()

	Config = GetDefaultConfig()
	Config.ConfigPath = "/tmp/test-overseer"

	got := GetSocketPath()
	want := filepath.Join("/tmp/test-overseer", SocketName)
	if got != want {
		t.Errorf("GetSocketPath() = %q, want %q", got, want)
	}
}

func TestGetPIDFilePath(t *testing.T) {
	original := Config
	defer func() { Config = original }()

	Config = GetDefaultConfig()
	Config.ConfigPath = "/tmp/test-overseer"

	got := GetPIDFilePath()
	want := filepath.Join("/tmp/test-overseer", PidFileName)
	if got != want {
		t.Errorf("GetPIDFilePath() = %q, want %q", got, want)
	}
}

func TestConstants(t *testing.T) {
	if BaseDirName != ".config/overseer" {
		t.Errorf("BaseDirName = %q, want %q", BaseDirName, ".config/overseer")
	}
	if PidFileName != "daemon.pid" {
		t.Errorf("PidFileName = %q, want %q", PidFileName, "daemon.pid")
	}
	if SocketName != "daemon.sock" {
		t.Errorf("SocketName = %q, want %q", SocketName, "daemon.sock")
	}
}

func TestWriteDefaultHCLConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.hcl")

	err := writeDefaultHCLConfig(path)
	if err != nil {
		t.Fatalf("writeDefaultHCLConfig() error: %v", err)
	}

	// The generated config should be parseable
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("Failed to parse default config: %v", err)
	}

	if cfg.SSH.ServerAliveInterval != 15 {
		t.Errorf("Expected SSH.ServerAliveInterval=15, got %d", cfg.SSH.ServerAliveInterval)
	}
}
