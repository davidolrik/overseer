package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/daemon"
)

func TestFormatEnvInfo(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "nil environment",
			env:  nil,
			want: "",
		},
		{
			name: "empty environment",
			env:  map[string]string{},
			want: "",
		},
		{
			name: "only OVERSEER_ variables",
			env: map[string]string{
				"OVERSEER_CONTEXT":   "home",
				"OVERSEER_LOCATION":  "living-room",
				"OVERSEER_PUBLIC_IP": "1.2.3.4",
			},
			want: "",
		},
		{
			name: "single user variable",
			env: map[string]string{
				"MY_VAR": "hello",
			},
			want: " \033[2m[MY_VAR=hello]\033[0m",
		},
		{
			name: "multiple user variables sorted",
			env: map[string]string{
				"ZOO":  "zebra",
				"ALPHA": "first",
			},
			want: " \033[2m[ALPHA=first, ZOO=zebra]\033[0m",
		},
		{
			name: "mixed user and OVERSEER_ variables",
			env: map[string]string{
				"OVERSEER_CONTEXT": "office",
				"MY_VAR":          "value",
				"OVERSEER_TAG":    "abc",
				"OTHER":           "stuff",
			},
			want: " \033[2m[MY_VAR=value, OTHER=stuff]\033[0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatEnvInfo(tt.env)
			if got != tt.want {
				t.Errorf("formatEnvInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatAuthInfo(t *testing.T) {
	t.Setenv("HOME", "/Users/djo")

	tests := []struct {
		name     string
		identity string
		method   string
		reused   bool
		want     string
	}{
		{
			name:     "nothing known",
			identity: "",
			method:   "",
			want:     "",
		},
		{
			name:   "reused master owned by someone else",
			reused: true,
			want:   "\033[90mreused\033[0m",
		},
		{
			name:     "reused master owned by a tunnel with a key",
			identity: "/Users/djo/.ssh/id_jumphost_only",
			method:   "publickey",
			reused:   true,
			want:     "\033[90mKey:\033[0m id_jumphost_only\033[90m, reused\033[0m",
		},
		{
			name:   "reused master owned by a tunnel using a password",
			method: "keyboard-interactive",
			reused: true,
			want:   "\033[90mAuth:\033[0m password\033[90m, reused\033[0m",
		},
		{
			name:     "password auth shows method",
			identity: "",
			method:   "password",
			want:     "\033[90mAuth:\033[0m password",
		},
		{
			name:     "keyboard-interactive is how ssh delivers the keychain password, shown as password",
			identity: "",
			method:   "keyboard-interactive",
			want:     "\033[90mAuth:\033[0m password",
		},
		{
			name:     "other methods are shown as reported",
			identity: "",
			method:   "gssapi-with-mic",
			want:     "\033[90mAuth:\033[0m gssapi-with-mic",
		},
		{
			name:     "publickey without a parsed identity shows method",
			identity: "",
			method:   "publickey",
			want:     "\033[90mAuth:\033[0m publickey",
		},
		{
			name:     "identity takes precedence over method",
			identity: "/Users/djo/.ssh/id_ed25519",
			method:   "publickey",
			want:     "\033[90mKey:\033[0m id_ed25519",
		},
		{
			name:     "key in default ssh directory shows bare name",
			identity: "/Users/djo/.ssh/id_ed25519",
			want:     "\033[90mKey:\033[0m id_ed25519",
		},
		{
			name:     "public key file used with an agent drops the .pub suffix",
			identity: "/Users/djo/.ssh/id_ed25519.pub",
			want:     "\033[90mKey:\033[0m id_ed25519",
		},
		{
			name:     "key in subdirectory of ssh directory keeps relative path",
			identity: "/Users/djo/.ssh/work/id_ed25519",
			want:     "\033[90mKey:\033[0m work/id_ed25519",
		},
		{
			name:     "key elsewhere under home is shortened with tilde",
			identity: "/Users/djo/keys/deploy",
			want:     "\033[90mKey:\033[0m ~/keys/deploy",
		},
		{
			name:     "key outside home is shown as-is",
			identity: "/etc/ssh/shared_key",
			want:     "\033[90mKey:\033[0m /etc/ssh/shared_key",
		},
		{
			name:     "agent key comment is shown as-is",
			identity: "Work Laptop Key",
			want:     "\033[90mKey:\033[0m Work Laptop Key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := daemon.HopAuth{Identity: tt.identity, Method: tt.method, Reused: tt.reused}
			got := formatAuthInfo(auth)
			if got != tt.want {
				t.Errorf("formatAuthInfo(%+v) = %q, want %q", auth, got, tt.want)
			}
		})
	}
}

// captureStdout runs fn and returns what it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()
	w.Close()

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestDisplayTunnels_ShowsAuthNextToEachHop(t *testing.T) {
	t.Setenv("HOME", "/Users/djo")
	now := time.Now().Format(time.RFC3339)

	statuses := []daemon.DaemonStatus{
		{
			Hostname:          "deep",
			Pid:               4521,
			State:             "connected",
			LastConnectedTime: now,
			JumpChain:         []string{"gate.example.com:22", "dmz.example.com:22", "10.10.1.50:22"},
			HopAuths: []daemon.HopAuth{
				{Identity: "/Users/djo/.ssh/id_jump", Method: "publickey", Reused: true},
				{Method: "keyboard-interactive"},
				{Identity: "/Users/djo/.ssh/id_ed25519.pub", Method: "publickey"},
			},
		},
	}

	out := stripANSI(captureStdout(t, func() { displayTunnels(statuses, nil) }))

	want := []string{
		"  └── → gate.example.com:22 (Key: id_jump, reused)\n",
		"      └── → dmz.example.com:22 (Auth: password)\n",
		"          └── → 10.10.1.50:22 (Key: id_ed25519)\n",
	}
	for _, line := range want {
		if !strings.Contains(out, line) {
			t.Errorf("expected output to contain %q, got:\n%s", line, out)
		}
	}
	if strings.Contains(out, "Key:") && strings.Contains(strings.SplitN(out, "\n", 2)[0], "Key:") {
		t.Errorf("did not expect Key on the tunnel line, got:\n%s", out)
	}
	if !strings.Contains(out, "✓ deep (PID: 4521, Age: ") {
		t.Errorf("unexpected tunnel line, got:\n%s", out)
	}
}

func TestDisplayTunnels_DestinationAuthMapsToLastHop(t *testing.T) {
	t.Setenv("HOME", "/Users/djo")
	now := time.Now().Format(time.RFC3339)

	// Only the destination's authentication is known (e.g. a ProxyCommand
	// that isn't ssh), so only the last hop is annotated.
	statuses := []daemon.DaemonStatus{
		{
			Hostname:          "partial",
			Pid:               1,
			State:             "connected",
			LastConnectedTime: now,
			JumpChain:         []string{"gate.example.com:22", "10.10.1.50:22"},
			HopAuths:          []daemon.HopAuth{{Identity: "/Users/djo/.ssh/id_ed25519", Method: "publickey"}},
		},
	}

	out := stripANSI(captureStdout(t, func() { displayTunnels(statuses, nil) }))

	if !strings.Contains(out, "  └── → gate.example.com:22\n") {
		t.Errorf("expected first hop without auth info, got:\n%s", out)
	}
	if !strings.Contains(out, "      └── → 10.10.1.50:22 (Key: id_ed25519)\n") {
		t.Errorf("expected key on the last hop, got:\n%s", out)
	}
}

// When a ControlMaster reuse collapses several config hops into a single
// reused entry, jump_chain (from ssh -G) is longer than hop_auths (actual
// connection). The first auth is the reused jump and belongs on the first
// hop; the destination auth belongs on the last hop; the phantom middle hop
// is left unannotated. This mirrors the real p-pg01 topology.
func TestDisplayTunnels_ReuseCollapsedChainAnnotatesFirstAndLast(t *testing.T) {
	t.Setenv("HOME", "/Users/djo")
	now := time.Now().Format(time.RFC3339)

	statuses := []daemon.DaemonStatus{
		{
			Hostname:          "p-pg01",
			Pid:               1,
			State:             "connected",
			LastConnectedTime: now,
			JumpChain:         []string{"172.16.44.10:64242", "p-gate03.olrik.cloud:64242", "p-pg01.olrik.cloud:64242"},
			HopAuths: []daemon.HopAuth{
				{Reused: true, ControlPath: "/Users/djo/.ssh/control/p-jump03__64242__djo"},
				{Identity: "/Users/djo/.ssh/id_ed25519", Method: "publickey"},
			},
		},
	}

	out := stripANSI(captureStdout(t, func() { displayTunnels(statuses, nil) }))

	want := []string{
		"  └── → 172.16.44.10:64242 (reused)\n",
		"      └── → p-gate03.olrik.cloud:64242\n",
		"          └── → p-pg01.olrik.cloud:64242 (Key: id_ed25519)\n",
	}
	for _, line := range want {
		if !strings.Contains(out, line) {
			t.Errorf("expected output to contain %q, got:\n%s", line, out)
		}
	}
	// The phantom middle hop must carry no annotation.
	if strings.Contains(out, "p-gate03.olrik.cloud:64242 (") {
		t.Errorf("did not expect annotation on the collapsed middle hop, got:\n%s", out)
	}
}

func TestDisplayTunnels_SingleHopShowsAuthOnResolvedHost(t *testing.T) {
	t.Setenv("HOME", "/Users/djo")
	now := time.Now().Format(time.RFC3339)

	statuses := []daemon.DaemonStatus{
		{
			Hostname:          "direct",
			Pid:               2,
			State:             "connected",
			LastConnectedTime: now,
			ResolvedHost:      "198.51.100.50:22",
			HopAuths:          []daemon.HopAuth{{Method: "keyboard-interactive"}},
		},
	}

	out := stripANSI(captureStdout(t, func() { displayTunnels(statuses, nil) }))

	if !strings.Contains(out, "  └── → 198.51.100.50:22 (Auth: password)\n") {
		t.Errorf("expected auth on the single hop, got:\n%s", out)
	}
}
