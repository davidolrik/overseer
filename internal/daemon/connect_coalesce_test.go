package daemon

import (
	"strings"
	"testing"

	"go.olrik.dev/overseer/internal/core"
)

// setupCoalesceDaemon returns a daemon with empty config so startTunnelStreaming
// can run without a real orchestrator or tunnels.
func setupCoalesceDaemon(t *testing.T) *Daemon {
	t.Helper()
	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}
	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	return New()
}

func onlyMessage(t *testing.T, resp Response) ResponseMessage {
	t.Helper()
	if len(resp.Messages) != 1 {
		t.Fatalf("expected exactly 1 message, got %d: %+v", len(resp.Messages), resp.Messages)
	}
	return resp.Messages[0]
}

// A connect that arrives while another is already in flight for the same alias
// must coalesce: report success (INFO), not spawn a second ssh.
func TestStartTunnelStreaming_CoalescesInFlightConnect(t *testing.T) {
	quietLogger(t)
	d := setupCoalesceDaemon(t)

	// Simulate another connect in progress.
	d.connecting["myhost"] = true

	resp := d.startTunnelStreaming("myhost", nil, nil, false)

	msg := onlyMessage(t, resp)
	if msg.Status != "INFO" {
		t.Errorf("expected INFO status, got %q (%q)", msg.Status, msg.Message)
	}
	if !strings.Contains(strings.ToLower(msg.Message), "in progress") {
		t.Errorf("expected an 'in progress' message, got %q", msg.Message)
	}
	// The in-flight flag belongs to the other connect and must be left intact.
	if !d.connecting["myhost"] {
		t.Error("must not clear another connect's in-flight flag")
	}
}

// An entry mid-connect (StateConnecting/StateReconnecting, e.g. the monitor's
// auto-reconnect) must coalesce rather than health-check-and-respawn — the
// TCP isn't up yet, so a health check would wrongly read it as stale.
func TestStartTunnelStreaming_CoalescesConnectingEntry(t *testing.T) {
	quietLogger(t)
	for _, state := range []TunnelState{StateConnecting, StateReconnecting} {
		t.Run(string(state), func(t *testing.T) {
			d := setupCoalesceDaemon(t)
			d.tunnels["myhost"] = Tunnel{Hostname: "myhost", Pid: 999999999, State: state}

			resp := d.startTunnelStreaming("myhost", nil, nil, false)

			msg := onlyMessage(t, resp)
			if msg.Status != "INFO" {
				t.Errorf("expected INFO status, got %q (%q)", msg.Status, msg.Message)
			}
			if !strings.Contains(strings.ToLower(msg.Message), "in progress") {
				t.Errorf("expected an 'in progress' message, got %q", msg.Message)
			}
			// The existing entry must be preserved, not deleted-and-respawned.
			if _, exists := d.tunnels["myhost"]; !exists {
				t.Error("must not remove the in-progress tunnel entry")
			}
		})
	}
}

// After a failed connect, the in-flight reservation must be cleared so a retry
// can proceed. Uses a trailing-dot alias so ssh fails fast.
func TestStartTunnelStreaming_ClearsReservationAfterFailure(t *testing.T) {
	quietLogger(t)
	d := setupCoalesceDaemon(t)

	resp := d.startTunnelStreaming("no-such-host.", nil, nil, false)
	_ = resp

	d.mu.Lock()
	reserved := d.connecting["no-such-host."]
	d.mu.Unlock()
	if reserved {
		t.Error("expected the in-flight reservation to be cleared after the connect returned")
	}
}
