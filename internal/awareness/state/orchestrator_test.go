package state

import (
	"log/slog"
	"net"
	"os"
	"testing"
	"time"
)

// setManagerState is a test helper that directly sets the state manager's current state.
func setManagerState(m *StateManager, s StateSnapshot) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	m.currentState = s
}

func TestNewOrchestrator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Rules: []Rule{
			{
				Name:        "untrusted",
				DisplayName: "Untrusted",
				Conditions:  map[string][]string{},
			},
		},
		Locations: map[string]Location{
			"unknown": {
				Name:        "unknown",
				DisplayName: "Unknown",
			},
		},
		Logger: logger,
	})

	if o == nil {
		t.Fatal("expected non-nil orchestrator")
	}
	if o.manager == nil {
		t.Error("expected manager to be initialized")
	}
	if o.streamer == nil {
		t.Error("expected streamer to be initialized")
	}
	if o.ruleEngine == nil {
		t.Error("expected rule engine to be initialized")
	}
	if o.effects == nil {
		t.Error("expected effects processor to be initialized")
	}
}

func TestNewOrchestrator_DefaultConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	if o == nil {
		t.Fatal("expected non-nil orchestrator")
	}
}

func TestOrchestrator_StartStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Rules: []Rule{
			{Name: "untrusted", DisplayName: "Untrusted"},
		},
		Locations: map[string]Location{
			"unknown": {Name: "unknown", DisplayName: "Unknown"},
		},
		Logger: logger,
	})

	o.Start()

	// Give it a moment to stabilize
	time.Sleep(100 * time.Millisecond)

	o.Stop()
}

func TestOrchestrator_GetCurrentState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	state := o.GetCurrentState()
	// Initial state should have default values
	if state.Online {
		t.Error("expected Online=false initially")
	}
}

func TestOrchestrator_IsOnline(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	if o.IsOnline() {
		t.Error("expected IsOnline=false initially")
	}
}

func TestOrchestrator_GetLogStreamer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	if o.GetLogStreamer() == nil {
		t.Error("expected non-nil log streamer")
	}
}

func TestOrchestrator_GetRuleEngine(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Rules:  []Rule{{Name: "test"}},
		Logger: logger,
	})

	re := o.GetRuleEngine()
	if re == nil {
		t.Error("expected non-nil rule engine")
	}
}

func TestOrchestrator_GetCurrentRule(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	rule := o.GetCurrentRule()
	if rule != nil {
		t.Error("expected nil current rule initially")
	}
}

func TestOrchestrator_HasEnvWriters(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	t.Run("no writers", func(t *testing.T) {
		o := NewOrchestrator(OrchestratorConfig{
			Logger: logger,
		})
		if o.HasEnvWriters() {
			t.Error("expected HasEnvWriters=false")
		}
	})
}

func TestOrchestrator_LastWrittenPublicIPv4(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	if ip := o.LastWrittenPublicIPv4(); ip != "" {
		t.Errorf("expected empty string initially, got %q", ip)
	}
}

func TestOrchestrator_IsSuppressed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	if o.IsSuppressed() {
		t.Error("expected IsSuppressed=false initially")
	}
}

func TestOrchestrator_SubscribeLogs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	id, ch := o.SubscribeLogs(false)
	if ch == nil {
		t.Fatal("expected non-nil log channel")
	}

	// Emit an event and verify it arrives
	o.EmitSystemEvent("test_event", "test details")
	select {
	case entry := <-ch:
		if entry.Category != CategorySystem {
			t.Errorf("expected system category, got %v", entry.Category)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for log entry")
	}

	o.UnsubscribeLogs(id)
}

func TestOrchestrator_SubscribeLogsWithHistory(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	// Emit some events first
	o.EmitSystemEvent("event1", "details1")
	o.EmitSystemEvent("event2", "details2")

	id, ch := o.SubscribeLogsWithHistory(true, 10)
	if ch == nil {
		t.Fatal("expected non-nil log channel")
	}

	// Should receive replayed history
	count := 0
	timeout := time.After(time.Second)
loop:
	for {
		select {
		case <-ch:
			count++
		case <-timeout:
			break loop
		}
	}
	if count < 2 {
		t.Errorf("expected at least 2 replayed entries, got %d", count)
	}

	o.UnsubscribeLogs(id)
}

func TestOrchestrator_SubscribeState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	ch := make(chan StateSnapshot, 1)
	o.SubscribeState(func(s StateSnapshot) {
		select {
		case ch <- s:
		default:
		}
	})

	// The subscription is registered; we can't easily trigger it without Start()
	// but we can verify it doesn't panic
}

func TestOrchestrator_SetHookEventLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	// Should not panic
	o.SetHookEventLogger(func(identifier, eventType, details string) error {
		return nil
	})
}

func TestOrchestrator_SensorCacheRoundTrip(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	o := NewOrchestrator(OrchestratorConfig{
		Logger: logger,
	})

	online := true
	o.RestoreSensorCache([]SensorCacheEntry{
		{
			Sensor:    "tcp",
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Online:    &online,
		},
	})

	cache := o.GetSensorCache()
	if len(cache) != 1 {
		t.Fatalf("expected 1 cache entry, got %d", len(cache))
	}
	if cache[0].Sensor != "tcp" {
		t.Errorf("expected 'tcp', got %q", cache[0].Sensor)
	}
}

func TestOrchestrator_BuildSSHEnv(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.Level(99)}))

	t.Run("full state", func(t *testing.T) {
		o := NewOrchestrator(OrchestratorConfig{
			PreferredIP: "ipv4",
			Logger:      logger,
		})

		// Inject known state via sensor cache
		setManagerState(o.manager, StateSnapshot{
			Context:             "office",
			ContextDisplayName:  "Office",
			Location:            "hq",
			LocationDisplayName: "Headquarters",
			PublicIPv4:          net.ParseIP("203.0.113.1"),
			PublicIPv6:          net.ParseIP("2001:db8::1"),
			LocalIPv4:           net.ParseIP("192.168.1.50"),
			Environment: map[string]string{
				"MY_CUSTOM": "hello",
			},
		})

		env := o.BuildSSHEnv()

		expected := map[string]string{
			"OVERSEER_CONTEXT":              "office",
			"OVERSEER_CONTEXT_DISPLAY_NAME": "Office",
			"OVERSEER_LOCATION":             "hq",
			"OVERSEER_LOCATION_DISPLAY_NAME": "Headquarters",
			"OVERSEER_PUBLIC_IP":            "203.0.113.1",
			"OVERSEER_PUBLIC_IPV4":          "203.0.113.1",
			"OVERSEER_PUBLIC_IPV6":          "2001:db8::1",
			"OVERSEER_LOCAL_IP":             "192.168.1.50",
			"OVERSEER_LOCAL_IPV4":           "192.168.1.50",
			"MY_CUSTOM":                     "hello",
		}

		for key, want := range expected {
			got, ok := env[key]
			if !ok {
				t.Errorf("missing key %q", key)
			} else if got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}

		// Verify no unexpected keys
		if len(env) != len(expected) {
			t.Errorf("env has %d keys, want %d", len(env), len(expected))
			for k, v := range env {
				if _, ok := expected[k]; !ok {
					t.Errorf("unexpected key %q = %q", k, v)
				}
			}
		}
	})

	t.Run("preferred IP ipv6", func(t *testing.T) {
		o := NewOrchestrator(OrchestratorConfig{
			PreferredIP: "ipv6",
			Logger:      logger,
		})

		setManagerState(o.manager, StateSnapshot{
			PublicIPv4: net.ParseIP("203.0.113.1"),
			PublicIPv6: net.ParseIP("2001:db8::1"),
		})

		env := o.BuildSSHEnv()

		if env["OVERSEER_PUBLIC_IP"] != "2001:db8::1" {
			t.Errorf("OVERSEER_PUBLIC_IP = %q, want %q (ipv6 preferred)", env["OVERSEER_PUBLIC_IP"], "2001:db8::1")
		}
	})

	t.Run("preferred IP fallback to ipv6 when no ipv4", func(t *testing.T) {
		o := NewOrchestrator(OrchestratorConfig{
			PreferredIP: "ipv4",
			Logger:      logger,
		})

		setManagerState(o.manager, StateSnapshot{
			PublicIPv6: net.ParseIP("2001:db8::1"),
		})

		env := o.BuildSSHEnv()

		if env["OVERSEER_PUBLIC_IP"] != "2001:db8::1" {
			t.Errorf("OVERSEER_PUBLIC_IP = %q, want %q (fallback to ipv6)", env["OVERSEER_PUBLIC_IP"], "2001:db8::1")
		}
	})

	t.Run("empty state", func(t *testing.T) {
		o := NewOrchestrator(OrchestratorConfig{
			Logger: logger,
		})

		env := o.BuildSSHEnv()

		if len(env) != 0 {
			t.Errorf("expected empty env for empty state, got %d keys: %v", len(env), env)
		}
	})

	t.Run("custom env overrides OVERSEER_ prefix", func(t *testing.T) {
		o := NewOrchestrator(OrchestratorConfig{
			Logger: logger,
		})

		setManagerState(o.manager, StateSnapshot{
			Context: "office",
			Environment: map[string]string{
				"OVERSEER_CONTEXT": "custom-override",
			},
		})

		env := o.BuildSSHEnv()

		// Custom env applied after OVERSEER_ vars, so it wins
		if env["OVERSEER_CONTEXT"] != "custom-override" {
			t.Errorf("OVERSEER_CONTEXT = %q, want %q (custom env should override)", env["OVERSEER_CONTEXT"], "custom-override")
		}
	})
}
