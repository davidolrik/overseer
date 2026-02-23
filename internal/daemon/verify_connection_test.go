package daemon

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// setupDaemonForVerify creates a Daemon with a tunnel entry so verifyConnection can update ResolvedHost.
func setupDaemonForVerify(t *testing.T, alias string) *Daemon {
	t.Helper()
	d := &Daemon{
		tunnels:       map[string]Tunnel{alias: {Hostname: alias, State: StateConnecting}},
		askpassTokens: make(map[string]string),
	}
	return d
}

// writeLines writes each line to the writer with a newline, then closes it.
func writeLines(w io.WriteCloser, lines ...string) {
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
	w.Close()
}

func TestVerifyConnection_Success(t *testing.T) {
	quietLogger(t)
	d := setupDaemonForVerify(t, "myhost")

	r, w := io.Pipe()
	result := make(chan error, 1)
	go d.verifyConnection(r, "myhost", result)

	go writeLines(w,
		"debug1: Authenticated to myhost ([1.2.3.4]:22).",
		"debug1: Entering interactive session.",
	)

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}

	d.mu.Lock()
	resolved := d.tunnels["myhost"].ResolvedHost
	d.mu.Unlock()

	if resolved != "1.2.3.4:22" {
		t.Errorf("expected ResolvedHost '1.2.3.4:22', got %q", resolved)
	}
}

func TestVerifyConnection_PledgeNetwork(t *testing.T) {
	quietLogger(t)
	d := setupDaemonForVerify(t, "pledgehost")

	r, w := io.Pipe()
	result := make(chan error, 1)
	go d.verifyConnection(r, "pledgehost", result)

	go writeLines(w,
		"debug1: Authentication succeeded (publickey).",
		"debug1: pledge: network",
	)

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}
}

func TestVerifyConnection_PermissionDenied(t *testing.T) {
	quietLogger(t)
	d := setupDaemonForVerify(t, "denied")

	r, w := io.Pipe()
	result := make(chan error, 1)
	go d.verifyConnection(r, "denied", result)

	go writeLines(w,
		"debug1: Trying private key: /home/user/.ssh/id_ed25519",
		"debug1: Permission denied (publickey).",
	)

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "authentication failed") {
			t.Errorf("expected 'authentication failed', got %q", err.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}
}

func TestVerifyConnection_ConnectionRefused(t *testing.T) {
	quietLogger(t)
	d := setupDaemonForVerify(t, "refused")

	r, w := io.Pipe()
	result := make(chan error, 1)
	go d.verifyConnection(r, "refused", result)

	go writeLines(w,
		"ssh: connect to host refused port 22: Connection refused",
	)

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("expected 'connection refused', got %q", err.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}
}

func TestVerifyConnection_ConnectionTimeout(t *testing.T) {
	quietLogger(t)
	d := setupDaemonForVerify(t, "timeout")

	r, w := io.Pipe()
	result := make(chan error, 1)
	go d.verifyConnection(r, "timeout", result)

	go writeLines(w,
		"ssh: connect to host timeout port 22: Connection timed out",
	)

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "connection timed out") {
			t.Errorf("expected 'connection timed out', got %q", err.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}
}

func TestVerifyConnection_HostKeyFailed(t *testing.T) {
	quietLogger(t)
	d := setupDaemonForVerify(t, "hostkey")

	r, w := io.Pipe()
	result := make(chan error, 1)
	go d.verifyConnection(r, "hostkey", result)

	go writeLines(w,
		"@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@",
		"Host key verification failed.",
	)

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "host key verification failed") {
			t.Errorf("expected 'host key verification failed', got %q", err.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}
}

func TestVerifyConnection_PipeClosed(t *testing.T) {
	quietLogger(t)
	d := setupDaemonForVerify(t, "closed")

	r, w := io.Pipe()
	result := make(chan error, 1)
	go d.verifyConnection(r, "closed", result)

	// Close the writer immediately — scanner.Scan() returns false
	w.Close()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "SSH process terminated unexpectedly") {
			t.Errorf("expected 'SSH process terminated unexpectedly', got %q", err.Error())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}
}

func TestVerifyConnection_ProxyHop(t *testing.T) {
	quietLogger(t)
	d := setupDaemonForVerify(t, "proxied")

	r, w := io.Pipe()
	result := make(chan error, 1)
	go d.verifyConnection(r, "proxied", result)

	go writeLines(w,
		"debug1: Authenticating to jump.example.com:2222 as 'admin'",
		"debug1: Authenticated to proxied (via jump.example.com)",
		"debug1: Entering interactive session.",
	)

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}

	d.mu.Lock()
	resolved := d.tunnels["proxied"].ResolvedHost
	d.mu.Unlock()

	// When "Authenticated to" says "(via proxy)" without IP:port, the code falls back
	// to the host:port from the prior "Authenticating to" line.
	if resolved != "jump.example.com:2222" {
		t.Errorf("expected ResolvedHost 'jump.example.com:2222', got %q", resolved)
	}
}
