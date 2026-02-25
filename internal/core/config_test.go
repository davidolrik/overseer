package core

import (
	"crypto/sha256"
	"fmt"
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

func TestProcessTag(t *testing.T) {
	original := Config
	defer func() { Config = original }()

	t.Run("returns 8-char hex string", func(t *testing.T) {
		Config = GetDefaultConfig()
		Config.ConfigPath = "/home/alice/.config/overseer"

		tag := ProcessTag()
		if len(tag) != 8 {
			t.Errorf("expected 8-char tag, got %d chars: %q", len(tag), tag)
		}
		// Verify it's valid hex
		for _, c := range tag {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("tag contains non-hex char %q: %q", string(c), tag)
			}
		}
	})

	t.Run("deterministic for same path", func(t *testing.T) {
		Config = GetDefaultConfig()
		Config.ConfigPath = "/home/alice/.config/overseer"

		tag1 := ProcessTag()
		tag2 := ProcessTag()
		if tag1 != tag2 {
			t.Errorf("expected same tag for same path, got %q and %q", tag1, tag2)
		}
	})

	t.Run("different paths produce different tags", func(t *testing.T) {
		Config = GetDefaultConfig()
		Config.ConfigPath = "/home/alice/.config/overseer"
		tagAlice := ProcessTag()

		Config.ConfigPath = "/home/bob/.config/overseer"
		tagBob := ProcessTag()

		if tagAlice == tagBob {
			t.Errorf("expected different tags for different paths, both got %q", tagAlice)
		}
	})

	t.Run("matches expected SHA-256 prefix", func(t *testing.T) {
		Config = GetDefaultConfig()
		Config.ConfigPath = "/home/alice/.config/overseer"

		tag := ProcessTag()
		hash := sha256.Sum256([]byte("/home/alice/.config/overseer"))
		expected := fmt.Sprintf("%x", hash[:4])
		if tag != expected {
			t.Errorf("expected %q, got %q", expected, tag)
		}
	})
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
