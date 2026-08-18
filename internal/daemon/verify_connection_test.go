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

// runVerifyConnection feeds lines to verifyConnection and returns the tunnel entry afterwards.
func runVerifyConnection(t *testing.T, alias string, lines ...string) Tunnel {
	t.Helper()
	d := setupDaemonForVerify(t, alias)

	r, w := io.Pipe()
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.verifyConnection(r, alias, result)
	}()

	go writeLines(w, lines...)

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection result")
	}

	// verifyConnection keeps draining stderr (and recording hops) after it
	// reports success; wait for it to finish reading the closed pipe.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for verifyConnection to finish")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tunnels[alias]
}

// wantHopAuths fails the test unless the tunnel's HopAuths match exactly.
func wantHopAuths(t *testing.T, tunnel Tunnel, want []HopAuth) {
	t.Helper()
	if len(tunnel.HopAuths) != len(want) {
		t.Fatalf("expected %d hop auths, got %d: %+v", len(want), len(tunnel.HopAuths), tunnel.HopAuths)
	}
	for i := range want {
		if tunnel.HopAuths[i] != want[i] {
			t.Errorf("hop %d: expected %+v, got %+v", i, want[i], tunnel.HopAuths[i])
		}
	}
}

func TestVerifyConnection_RecordsIdentityFromAcceptedKey(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "keyed",
		"debug1: Offering public key: /Users/djo/.ssh/id_rsa RSA SHA256:aaaa explicit",
		"debug1: Offering public key: /Users/djo/.ssh/id_ed25519 ED25519 SHA256:bbbb explicit agent",
		"debug1: Server accepts key: /Users/djo/.ssh/id_ed25519 ED25519 SHA256:bbbb explicit agent",
		"debug1: Authentication succeeded (publickey).",
		"debug1: Authenticated to keyed ([1.2.3.4]:22).",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{{Identity: "/Users/djo/.ssh/id_ed25519", Method: "publickey"}})
}

func TestVerifyConnection_RecordsIdentityWithSpacesInAgentComment(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "agentkey",
		"debug1: Server accepts key: Work Laptop Key ED25519-SK SHA256:cccc authenticator agent",
		"debug1: Authentication succeeded (publickey).",
		"debug1: Authenticated to agentkey ([1.2.3.4]:22).",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{{Identity: "Work Laptop Key", Method: "publickey"}})
}

func TestVerifyConnection_RecordsIdentityFromPrivateKeyWithoutPub(t *testing.T) {
	quietLogger(t)
	// A private key with no matching .pub file is signed with directly and
	// never produces a "Server accepts key" line.
	tunnel := runVerifyConnection(t, "privkey",
		"debug1: Trying private key: /Users/djo/.ssh/legacy_key",
		"debug1: Authentication succeeded (publickey).",
		"debug1: Authenticated to privkey ([1.2.3.4]:22).",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{{Identity: "/Users/djo/.ssh/legacy_key", Method: "publickey"}})
}

func TestVerifyConnection_NoIdentityForPasswordAuth(t *testing.T) {
	quietLogger(t)
	// Keys offered and rejected before falling back to password must not be
	// reported as the identity in use.
	tunnel := runVerifyConnection(t, "pwhost",
		"debug1: Offering public key: /Users/djo/.ssh/id_rsa RSA SHA256:aaaa explicit",
		"debug1: Authentications that can continue: password",
		"debug1: Next authentication method: password",
		"debug1: Authentication succeeded (password).",
		"debug1: Authenticated to pwhost ([1.2.3.4]:22).",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{{Method: "password"}})
}

func TestVerifyConnection_RecordsKeyboardInteractiveAuthMethod(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "kbd",
		"debug1: Authentication succeeded (keyboard-interactive).",
		"debug1: Authenticated to kbd ([1.2.3.4]:22).",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{{Method: "keyboard-interactive"}})
}

func TestVerifyConnection_NoAuthMethodWhenNotReported(t *testing.T) {
	quietLogger(t)
	// Old-style output without a method on the "Authenticated to" line and
	// no "Authentication succeeded" line leaves the method unknown but still
	// records the hop.
	tunnel := runVerifyConnection(t, "unknown",
		"debug1: Authenticated to unknown ([1.2.3.4]:22).",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{{}})
}

// Newer OpenSSH releases report the auth method on the "Authenticated to"
// line ('using "publickey"') and no longer log "Authentication succeeded".
func TestVerifyConnection_RecordsIdentityWithUsingMethodFormat(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "modern",
		"debug1: Server accepts key: /Users/djo/.ssh/id_ed25519 ED25519 SHA256:bbbb explicit",
		"Authenticated to 127.0.0.1 ([127.0.0.1]:60315) using \"publickey\".",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{{Identity: "/Users/djo/.ssh/id_ed25519", Method: "publickey"}})
}

func TestVerifyConnection_NoIdentityForPasswordAuthWithUsingMethodFormat(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "modernpw",
		"debug1: Offering public key: /Users/djo/.ssh/id_rsa RSA SHA256:aaaa explicit",
		"debug1: Authentications that can continue: password",
		"Authenticated to 127.0.0.1 ([127.0.0.1]:22) using \"password\".",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{{Method: "password"}})
}

// With ProxyJump, ssh passes -v to the child ssh that connects to the jump
// host, so its output is interleaved with ours: the jump host authenticates
// (and its session opens) before the destination even starts authenticating.
// Every hop is recorded in connection order, jump hosts first.
func TestVerifyConnection_RecordsEveryHopInOrder(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "proxied",
		"debug1: Executing proxy command: exec ssh -v -W '[10.0.0.5]:22' jump",
		"debug1: Authenticating to jump.example.com:2222 as 'admin'",
		"debug1: Server accepts key: /Users/djo/.ssh/jump_key ED25519 SHA256:aaaa explicit",
		"Authenticated to jump.example.com ([10.0.0.1]:2222) using \"publickey\".",
		"debug1: Entering interactive session.",
		"debug1: Authenticating to 10.0.0.5:22 as 'djo'",
		"debug1: Server accepts key: /Users/djo/.ssh/dest_key ED25519 SHA256:bbbb explicit",
		"Authenticated to 10.0.0.5 (via proxy) using \"publickey\".",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{
		{Identity: "/Users/djo/.ssh/jump_key", Method: "publickey"},
		{Identity: "/Users/djo/.ssh/dest_key", Method: "publickey"},
	})
}

func TestVerifyConnection_RecordsEveryHopInOrderOldFormat(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "proxied-old",
		"debug1: Authenticating to jump.example.com:2222 as 'admin'",
		"debug1: Server accepts key: /Users/djo/.ssh/jump_key ED25519 SHA256:aaaa explicit",
		"debug1: Authentication succeeded (publickey).",
		"debug1: Authenticated to jump.example.com ([10.0.0.1]:2222).",
		"debug1: Entering interactive session.",
		"debug1: Authenticating to 10.0.0.5:22 as 'djo'",
		"debug1: Authentication succeeded (keyboard-interactive).",
		"debug1: Authenticated to 10.0.0.5 (via proxy).",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{
		{Identity: "/Users/djo/.ssh/jump_key", Method: "publickey"},
		{Method: "keyboard-interactive"},
	})
}

// A jump hop can go through an existing ControlMaster socket (e.g. one owned
// by another tunnel to the same host) instead of authenticating. The client
// side of that logs "mux_client_request_stdio_fwd: master session id: N"
// after naming the socket it tried. The parent's own failed probe of its
// socket must not count.
func TestVerifyConnection_RecordsReusedMuxHop(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "muxed",
		"debug1: auto-mux: Trying existing master at '/Users/djo/.ssh/control/dest__53764__demo'",
		"debug1: Control socket \"/Users/djo/.ssh/control/dest__53764__demo\" does not exist",
		"debug1: Executing proxy command: exec ssh -v -W '[127.0.0.1]:53764' jump",
		"debug1: auto-mux: Trying existing master at '/Users/djo/.ssh/control/jump__53763__demo'",
		"debug1: mux_client_request_stdio_fwd: master session id: 2",
		"debug1: Authenticating to 127.0.0.1:53764 as 'demo'",
		"debug1: Server accepts key: /Users/djo/.ssh/dest_key ED25519 SHA256:bbbb explicit",
		"Authenticated to 127.0.0.1 (via proxy) using \"publickey\".",
		"debug1: Entering interactive session.",
	)

	wantHopAuths(t, tunnel, []HopAuth{
		{Reused: true, ControlPath: "/Users/djo/.ssh/control/jump__53763__demo"},
		{Identity: "/Users/djo/.ssh/dest_key", Method: "publickey"},
	})
}

// When one of our tunnels is the ControlMaster that another connection
// reuses, the master logs "multiplexing control connection" on its own
// stderr. That is the master serving a client, not a hop of this tunnel.
func TestVerifyConnection_MasterServingClientIsNotAHop(t *testing.T) {
	quietLogger(t)
	tunnel := runVerifyConnection(t, "master",
		"debug1: auto-mux: Trying existing master at '/Users/djo/.ssh/control/jump__53763__demo'",
		"debug1: Control socket \"/Users/djo/.ssh/control/jump__53763__demo\" does not exist",
		"debug1: Server accepts key: /Users/djo/.ssh/jump_key ED25519 SHA256:aaaa explicit",
		"Authenticated to 127.0.0.1 ([127.0.0.1]:53763) using \"publickey\".",
		"debug1: setting up multiplex master socket",
		"debug1: Entering interactive session.",
		"debug1: multiplexing control connection",
	)

	wantHopAuths(t, tunnel, []HopAuth{{Identity: "/Users/djo/.ssh/jump_key", Method: "publickey"}})
}
