package daemon

import (
	"os"
	"testing"
)

func TestMatchesCommandLine(t *testing.T) {
	tests := []struct {
		name     string
		actual   string
		expected []string
		want     bool
	}{
		{
			name:     "ssh present with all expected args",
			actual:   "ssh server1 -N -o ExitOnForwardFailure=yes",
			expected: []string{"ssh", "server1", "-N", "-o", "ExitOnForwardFailure=yes"},
			want:     true,
		},
		{
			name:     "missing ssh keyword",
			actual:   "scp server1 -N",
			expected: []string{"ssh", "server1", "-N"},
			want:     false,
		},
		{
			name:     "ssh present but required arg missing",
			actual:   "ssh server1 -N",
			expected: []string{"ssh", "server1", "-N", "-o", "ExitOnForwardFailure=yes"},
			want:     false,
		},
		{
			name:     "-v in expected list is skipped",
			actual:   "ssh server1 -N",
			expected: []string{"ssh", "server1", "-N", "-v"},
			want:     true,
		},
		{
			name:     "ssh in expected list is skipped",
			actual:   "ssh server1 -N",
			expected: []string{"ssh", "server1", "-N"},
			want:     true,
		},
		{
			name:     "empty expected args",
			actual:   "ssh server1",
			expected: []string{},
			want:     true,
		},
		{
			name:     "case sensitive SSH uppercase",
			actual:   "SSH server1 -N",
			expected: []string{"ssh", "server1", "-N"},
			want:     false,
		},
		{
			name:     "empty actual string",
			actual:   "",
			expected: []string{"ssh", "server1"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesCommandLine(tt.actual, tt.expected)
			if got != tt.want {
				t.Errorf("matchesCommandLine(%q, %v) = %v, want %v",
					tt.actual, tt.expected, got, tt.want)
			}
		})
	}
}

func TestValidateTunnelProcess(t *testing.T) {
	t.Run("current process with wrong cmdline", func(t *testing.T) {
		// Use our own PID (process exists and is signalable) but with
		// an SSH cmdline that won't match.
		info := TunnelInfo{
			PID:     os.Getpid(),
			Alias:   "test-alias",
			Cmdline: []string{"ssh", "nonexistent-host", "-N", "-o", "ExitOnForwardFailure=yes"},
		}

		got := ValidateTunnelProcess(info)
		if got {
			t.Error("expected false: process exists but cmdline should not match SSH")
		}
	})

	t.Run("non-existent process", func(t *testing.T) {
		// PID 0 is never a user process; Signal(0) will fail
		info := TunnelInfo{
			PID:     0,
			Alias:   "test-alias",
			Cmdline: []string{"ssh", "host", "-N"},
		}

		got := ValidateTunnelProcess(info)
		if got {
			t.Error("expected false for PID 0")
		}
	})
}
