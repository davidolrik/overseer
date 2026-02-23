package state

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"
)

// mockRuleEvaluator returns a fixed RuleResult for testing
type mockRuleEvaluator struct {
	result RuleResult
}

func (m *mockRuleEvaluator) Evaluate(readings map[string]SensorReading, online bool) RuleResult {
	return m.result
}

func TestNewStateManager_DefaultConfig(t *testing.T) {
	m := NewStateManager(ManagerConfig{})

	if m.policy == nil {
		t.Fatal("expected default policy, got nil")
	}
	if m.policy.Name() != "tcp_priority" {
		t.Errorf("expected tcp_priority policy, got %s", m.policy.Name())
	}
	if cap(m.readings) != 256 {
		t.Errorf("expected readings buffer size 256, got %d", cap(m.readings))
	}
	if cap(m.transitions) != 64 {
		t.Errorf("expected transitions buffer size 64, got %d", cap(m.transitions))
	}
	if m.ruleEvaluator != nil {
		t.Error("expected nil rule evaluator")
	}
}

func TestNewStateManager_CustomConfig(t *testing.T) {
	policy := NewAnyOnlinePolicy()
	evaluator := &mockRuleEvaluator{}

	m := NewStateManager(ManagerConfig{
		Policy:                policy,
		RuleEvaluator:         evaluator,
		ReadingsBufferSize:    32,
		TransitionsBufferSize: 16,
		Logger:                slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})

	if m.policy.Name() != "any_online" {
		t.Errorf("expected any_online policy, got %s", m.policy.Name())
	}
	if cap(m.readings) != 32 {
		t.Errorf("expected readings buffer size 32, got %d", cap(m.readings))
	}
	if cap(m.transitions) != 16 {
		t.Errorf("expected transitions buffer size 16, got %d", cap(m.transitions))
	}
}

func TestNewStateManager_NilPolicyFallback(t *testing.T) {
	m := NewStateManager(ManagerConfig{Policy: nil})
	if m.policy == nil {
		t.Fatal("expected default policy for nil input")
	}
	if m.policy.Name() != "tcp_priority" {
		t.Errorf("expected tcp_priority, got %s", m.policy.Name())
	}
}

func TestStateManager_StartStop(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	m.Start()
	// Verify the manager is processing by submitting a reading
	ok := m.SubmitReading(SensorReading{
		Sensor:    "test",
		Timestamp: time.Now(),
		Value:     "hello",
	})
	if !ok {
		t.Error("expected SubmitReading to succeed")
	}

	m.Stop()

	// After stop, transitions channel should be closed
	_, open := <-m.Transitions()
	if open {
		t.Error("expected transitions channel to be closed after Stop")
	}
}

func TestStateManager_TCPReadingProducesOnlineTransition(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	online := true
	m.Readings() <- SensorReading{
		Sensor:    "tcp",
		Timestamp: time.Now(),
		Online:    &online,
	}

	select {
	case tr := <-m.Transitions():
		if !tr.To.Online {
			t.Error("expected Online=true in transition")
		}
		if !tr.HasChanged("online") {
			t.Error("expected 'online' in changed fields")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transition")
	}
}

func TestStateManager_IPReadingsInSnapshot(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	// Submit IPv4 reading
	m.Readings() <- SensorReading{
		Sensor:    "public_ipv4",
		Timestamp: time.Now(),
		IP:        net.ParseIP("1.2.3.4"),
	}

	select {
	case <-m.Transitions():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ipv4 transition")
	}

	// Submit IPv6 reading
	m.Readings() <- SensorReading{
		Sensor:    "public_ipv6",
		Timestamp: time.Now(),
		IP:        net.ParseIP("2001:db8::1"),
	}

	select {
	case <-m.Transitions():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ipv6 transition")
	}

	// Submit local IPv4 reading
	m.Readings() <- SensorReading{
		Sensor:    "local_ipv4",
		Timestamp: time.Now(),
		IP:        net.ParseIP("192.168.1.100"),
	}

	select {
	case <-m.Transitions():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for local_ipv4 transition")
	}

	state := m.GetCurrentState()
	if state.PublicIPv4 == nil || state.PublicIPv4.String() != "1.2.3.4" {
		t.Errorf("expected PublicIPv4=1.2.3.4, got %v", state.PublicIPv4)
	}
	if state.PublicIPv6 == nil || state.PublicIPv6.String() != "2001:db8::1" {
		t.Errorf("expected PublicIPv6=2001:db8::1, got %v", state.PublicIPv6)
	}
	if state.LocalIPv4 == nil || state.LocalIPv4.String() != "192.168.1.100" {
		t.Errorf("expected LocalIPv4=192.168.1.100, got %v", state.LocalIPv4)
	}
}

func TestStateManager_DuplicateReadingNoStateTransition(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	online := true
	reading := SensorReading{
		Sensor:    "tcp",
		Timestamp: time.Now(),
		Online:    &online,
	}

	// First reading should produce a transition (online goes from false to true)
	m.Readings() <- reading
	select {
	case <-m.Transitions():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first transition")
	}

	// Exact same reading again — sensor value unchanged AND state unchanged
	// The manager treats this as no state field change, so no transition emitted on the transitions channel
	m.Readings() <- reading

	select {
	case <-m.Transitions():
		t.Error("expected no transition for duplicate reading with identical state")
	case <-time.After(200 * time.Millisecond):
		// Expected: no transition
	}
}

func TestStateManager_Subscribe(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	ch := make(chan StateSnapshot, 1)
	m.Subscribe(func(s StateSnapshot) {
		ch <- s
	})

	m.Start()
	defer m.Stop()

	online := true
	m.Readings() <- SensorReading{
		Sensor:    "tcp",
		Timestamp: time.Now(),
		Online:    &online,
	}

	select {
	case snap := <-ch:
		if !snap.Online {
			t.Error("expected subscriber to receive Online=true snapshot")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscriber callback")
	}
}

func TestStateManager_GetCurrentState(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	// Initial state should be zero-value
	state := m.GetCurrentState()
	if state.Online {
		t.Error("expected initial Online=false")
	}

	// After feeding a reading, state should update
	online := true
	m.Readings() <- SensorReading{
		Sensor:    "tcp",
		Timestamp: time.Now(),
		Online:    &online,
	}

	// Wait for processing
	select {
	case <-m.Transitions():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}

	state = m.GetCurrentState()
	if !state.Online {
		t.Error("expected Online=true after TCP reading")
	}
}

func TestStateManager_SubmitReadingFullBuffer(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		ReadingsBufferSize: 1,
		Logger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	// Don't start the manager — readings won't be consumed

	// First submit should succeed (fills the buffer)
	ok := m.SubmitReading(SensorReading{Sensor: "test1", Timestamp: time.Now()})
	if !ok {
		t.Error("expected first SubmitReading to succeed")
	}

	// Second submit should fail (buffer full, no consumer)
	ok = m.SubmitReading(SensorReading{Sensor: "test2", Timestamp: time.Now()})
	if ok {
		t.Error("expected SubmitReading to return false on full buffer")
	}
}

func TestStateManager_ForceCheck(t *testing.T) {
	// ForceCheck is most useful after a config change when the rule evaluator
	// may produce a different result. We set up a rule evaluator that returns
	// a context, so the ForceCheck triggers a context change transition.
	evaluator := &mockRuleEvaluator{
		result: RuleResult{
			Context:  "forced",
			Location: "test-loc",
		},
	}

	m := NewStateManager(ManagerConfig{
		RuleEvaluator: evaluator,
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	m.ForceCheck("test_reload")

	select {
	case tr := <-m.Transitions():
		expected := "force_check:test_reload"
		if tr.Trigger != expected {
			t.Errorf("expected trigger %q, got %q", expected, tr.Trigger)
		}
		if tr.To.Context != "forced" {
			t.Errorf("expected context 'forced', got %q", tr.To.Context)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for force check transition")
	}
}

func TestStateManager_SensorCacheRoundTrip(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	online := true
	entries := []SensorCacheEntry{
		{
			Sensor:    "tcp",
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Online:    &online,
		},
		{
			Sensor:    "public_ipv4",
			Timestamp: time.Now().Format(time.RFC3339Nano),
			IP:        "8.8.8.8",
		},
		{
			Sensor:    "env_test",
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Value:     "hello",
		},
	}

	m.RestoreSensorCache(entries)

	// Verify state was evaluated from restored cache
	state := m.GetCurrentState()
	if !state.Online {
		t.Error("expected Online=true after restoring TCP reading")
	}
	if state.PublicIPv4 == nil || state.PublicIPv4.String() != "8.8.8.8" {
		t.Errorf("expected PublicIPv4=8.8.8.8, got %v", state.PublicIPv4)
	}

	// Get the cache back and verify round-trip
	cached := m.GetSensorCache()
	if len(cached) != 3 {
		t.Fatalf("expected 3 cache entries, got %d", len(cached))
	}

	found := map[string]bool{}
	for _, entry := range cached {
		found[entry.Sensor] = true
	}
	for _, name := range []string{"tcp", "public_ipv4", "env_test"} {
		if !found[name] {
			t.Errorf("expected sensor %q in cache", name)
		}
	}
}

func TestStateManager_RestoreSensorCache_InvalidTimestamp(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	entries := []SensorCacheEntry{
		{
			Sensor:    "test",
			Timestamp: "not-a-timestamp",
			Value:     "hello",
		},
	}

	// Should not panic; invalid timestamp falls back to time.Now()
	m.RestoreSensorCache(entries)

	cached := m.GetSensorCache()
	if len(cached) != 1 {
		t.Fatalf("expected 1 cache entry, got %d", len(cached))
	}
	if cached[0].Value != "hello" {
		t.Errorf("expected value 'hello', got %q", cached[0].Value)
	}
}

func TestStateManager_OnlineNilIPDefaultsToZero(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	// Go online without any IP sensors
	online := true
	m.Readings() <- SensorReading{
		Sensor:    "tcp",
		Timestamp: time.Now(),
		Online:    &online,
	}

	select {
	case <-m.Transitions():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}

	state := m.GetCurrentState()
	if state.PublicIPv4 == nil {
		t.Fatal("expected PublicIPv4 to default to 0.0.0.0 when online")
	}
	if state.PublicIPv4.String() != "0.0.0.0" {
		t.Errorf("expected 0.0.0.0, got %s", state.PublicIPv4.String())
	}
	if state.PublicIPv6 == nil {
		t.Fatal("expected PublicIPv6 to default to :: when online")
	}
	if state.LocalIPv4 == nil {
		t.Fatal("expected LocalIPv4 to default to 0.0.0.0 when online")
	}
}

func TestStateManager_RuleEvaluatorIntegration(t *testing.T) {
	evaluator := &mockRuleEvaluator{
		result: RuleResult{
			Context:             "office",
			ContextDisplayName:  "Office",
			Location:            "hq",
			LocationDisplayName: "HQ Office",
			MatchedRule:         "office_rule",
			Environment:         map[string]string{"VPN": "on"},
		},
	}

	m := NewStateManager(ManagerConfig{
		RuleEvaluator: evaluator,
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	m.Readings() <- SensorReading{
		Sensor:    "test",
		Timestamp: time.Now(),
		Value:     "trigger",
	}

	select {
	case tr := <-m.Transitions():
		if tr.To.Context != "office" {
			t.Errorf("expected context 'office', got %q", tr.To.Context)
		}
		if tr.To.Location != "hq" {
			t.Errorf("expected location 'hq', got %q", tr.To.Location)
		}
		if tr.To.ContextDisplayName != "Office" {
			t.Errorf("expected context display name 'Office', got %q", tr.To.ContextDisplayName)
		}
		if tr.To.LocationDisplayName != "HQ Office" {
			t.Errorf("expected location display name 'HQ Office', got %q", tr.To.LocationDisplayName)
		}
		if tr.To.MatchedRule != "office_rule" {
			t.Errorf("expected matched rule 'office_rule', got %q", tr.To.MatchedRule)
		}
		if tr.To.Environment["VPN"] != "on" {
			t.Errorf("expected VPN=on in environment, got %v", tr.To.Environment)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transition")
	}
}

func TestDetectChanges(t *testing.T) {
	m := NewStateManager(ManagerConfig{})

	tests := []struct {
		name    string
		old     StateSnapshot
		new     StateSnapshot
		changed []string
	}{
		{
			name:    "no changes",
			old:     StateSnapshot{Online: true},
			new:     StateSnapshot{Online: true},
			changed: nil,
		},
		{
			name:    "online changed",
			old:     StateSnapshot{Online: false},
			new:     StateSnapshot{Online: true},
			changed: []string{"online"},
		},
		{
			name:    "location changed",
			old:     StateSnapshot{Location: "home"},
			new:     StateSnapshot{Location: "office"},
			changed: []string{"location"},
		},
		{
			name:    "context changed",
			old:     StateSnapshot{Context: "trusted"},
			new:     StateSnapshot{Context: "untrusted"},
			changed: []string{"context"},
		},
		{
			name:    "ipv4 changed",
			old:     StateSnapshot{PublicIPv4: net.ParseIP("1.2.3.4")},
			new:     StateSnapshot{PublicIPv4: net.ParseIP("5.6.7.8")},
			changed: []string{"ipv4"},
		},
		{
			name:    "ipv6 changed",
			old:     StateSnapshot{PublicIPv6: net.ParseIP("::1")},
			new:     StateSnapshot{PublicIPv6: net.ParseIP("::2")},
			changed: []string{"ipv6"},
		},
		{
			name:    "local_ipv4 changed",
			old:     StateSnapshot{LocalIPv4: net.ParseIP("192.168.1.1")},
			new:     StateSnapshot{LocalIPv4: net.ParseIP("10.0.0.1")},
			changed: []string{"local_ipv4"},
		},
		{
			name: "multiple changes",
			old:  StateSnapshot{Online: false, Context: "a"},
			new:  StateSnapshot{Online: true, Context: "b"},
			changed: []string{"online", "context"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.detectChanges(tt.old, tt.new)
			if len(result) != len(tt.changed) {
				t.Errorf("expected %d changes, got %d: %v", len(tt.changed), len(result), result)
				return
			}
			for i, field := range tt.changed {
				if result[i] != field {
					t.Errorf("expected changed[%d]=%q, got %q", i, field, result[i])
				}
			}
		})
	}
}

func TestIPEqual(t *testing.T) {
	tests := []struct {
		name   string
		a, b   net.IP
		expect bool
	}{
		{"nil/nil", nil, nil, true},
		{"nil/non-nil", nil, net.ParseIP("1.2.3.4"), false},
		{"non-nil/nil", net.ParseIP("1.2.3.4"), nil, false},
		{"equal", net.ParseIP("1.2.3.4"), net.ParseIP("1.2.3.4"), true},
		{"unequal", net.ParseIP("1.2.3.4"), net.ParseIP("5.6.7.8"), false},
		{"v4 vs v6", net.ParseIP("::1"), net.ParseIP("0.0.0.1"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ipEqual(tt.a, tt.b); got != tt.expect {
				t.Errorf("ipEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.expect)
			}
		})
	}
}

func TestReadingsEqual(t *testing.T) {
	tests := []struct {
		name   string
		a, b   SensorReading
		expect bool
	}{
		{
			name:   "identical empty",
			a:      SensorReading{Sensor: "a"},
			b:      SensorReading{Sensor: "a"},
			expect: true,
		},
		{
			name:   "different sensor",
			a:      SensorReading{Sensor: "a"},
			b:      SensorReading{Sensor: "b"},
			expect: false,
		},
		{
			name:   "one online nil one not",
			a:      SensorReading{Sensor: "a", Online: boolPtr(true)},
			b:      SensorReading{Sensor: "a"},
			expect: false,
		},
		{
			name:   "both online same value",
			a:      SensorReading{Sensor: "a", Online: boolPtr(true)},
			b:      SensorReading{Sensor: "a", Online: boolPtr(true)},
			expect: true,
		},
		{
			name:   "online different values",
			a:      SensorReading{Sensor: "a", Online: boolPtr(true)},
			b:      SensorReading{Sensor: "a", Online: boolPtr(false)},
			expect: false,
		},
		{
			name:   "different IPs",
			a:      SensorReading{Sensor: "a", IP: net.ParseIP("1.1.1.1")},
			b:      SensorReading{Sensor: "a", IP: net.ParseIP("2.2.2.2")},
			expect: false,
		},
		{
			name:   "same IPs",
			a:      SensorReading{Sensor: "a", IP: net.ParseIP("1.1.1.1")},
			b:      SensorReading{Sensor: "a", IP: net.ParseIP("1.1.1.1")},
			expect: true,
		},
		{
			name:   "different values",
			a:      SensorReading{Sensor: "a", Value: "x"},
			b:      SensorReading{Sensor: "a", Value: "y"},
			expect: false,
		},
		{
			name:   "one error nil one not",
			a:      SensorReading{Sensor: "a", Error: errors.New("fail")},
			b:      SensorReading{Sensor: "a"},
			expect: false,
		},
		{
			name:   "both errors same message",
			a:      SensorReading{Sensor: "a", Error: errors.New("fail")},
			b:      SensorReading{Sensor: "a", Error: errors.New("fail")},
			expect: true,
		},
		{
			name:   "both errors different message",
			a:      SensorReading{Sensor: "a", Error: errors.New("fail1")},
			b:      SensorReading{Sensor: "a", Error: errors.New("fail2")},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readingsEqual(tt.a, tt.b); got != tt.expect {
				t.Errorf("readingsEqual() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestStateManager_MultipleReadingsOnlyMeaningfulChanges(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	// Go online
	online := true
	m.Readings() <- SensorReading{
		Sensor:    "tcp",
		Timestamp: time.Now(),
		Online:    &online,
	}
	select {
	case <-m.Transitions():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for online transition")
	}

	// Set an IP
	m.Readings() <- SensorReading{
		Sensor:    "public_ipv4",
		Timestamp: time.Now(),
		IP:        net.ParseIP("1.2.3.4"),
	}
	select {
	case tr := <-m.Transitions():
		if !tr.HasChanged("ipv4") {
			t.Error("expected ipv4 change")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ip transition")
	}

	// Same IP again — new sensor reading but state doesn't change
	m.Readings() <- SensorReading{
		Sensor:    "public_ipv4",
		Timestamp: time.Now(),
		IP:        net.ParseIP("1.2.3.4"),
	}
	select {
	case <-m.Transitions():
		t.Error("expected no transition for identical IP")
	case <-time.After(200 * time.Millisecond):
		// Expected: no transition on channel (state fields didn't change)
	}
}

func TestStateManager_GetSensorReadingReturnsNil(t *testing.T) {
	m := NewStateManager(ManagerConfig{})
	// As documented, GetSensorReading currently always returns nil
	if m.GetSensorReading("tcp") != nil {
		t.Error("expected GetSensorReading to return nil")
	}
}

func TestStateManager_IPFromValue(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	// Submit a reading with Value instead of IP field
	m.Readings() <- SensorReading{
		Sensor:    "public_ipv4",
		Timestamp: time.Now(),
		Value:     "9.9.9.9",
	}

	select {
	case <-m.Transitions():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}

	state := m.GetCurrentState()
	if state.PublicIPv4 == nil || state.PublicIPv4.String() != "9.9.9.9" {
		t.Errorf("expected PublicIPv4=9.9.9.9 from Value field, got %v", state.PublicIPv4)
	}
}

func TestStateManager_RestoreSensorCacheWithRuleEvaluator(t *testing.T) {
	evaluator := &mockRuleEvaluator{
		result: RuleResult{
			Context:  "home",
			Location: "home-office",
		},
	}

	m := NewStateManager(ManagerConfig{
		RuleEvaluator: evaluator,
		Logger:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	online := true
	m.RestoreSensorCache([]SensorCacheEntry{
		{
			Sensor:    "tcp",
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Online:    &online,
		},
	})

	state := m.GetCurrentState()
	if state.Context != "home" {
		t.Errorf("expected context 'home', got %q", state.Context)
	}
	if state.Location != "home-office" {
		t.Errorf("expected location 'home-office', got %q", state.Location)
	}
}

func TestStateManager_RestoreSensorCacheEmpty(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	// Restoring empty cache should not change state
	m.RestoreSensorCache([]SensorCacheEntry{})

	state := m.GetCurrentState()
	if state.Online {
		t.Error("expected Online=false after restoring empty cache")
	}
}

func TestStateManager_TransitionsChannelFull(t *testing.T) {
	m := NewStateManager(ManagerConfig{
		TransitionsBufferSize: 1,
		Logger:                slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})
	m.Start()
	defer m.Stop()

	// Submit multiple readings that each change state fields, without consuming transitions.
	// Each reading changes the IP, which changes the ipv4 state field.
	for i := 0; i < 5; i++ {
		m.Readings() <- SensorReading{
			Sensor:    "public_ipv4",
			Timestamp: time.Now(),
			IP:        net.ParseIP(fmt.Sprintf("10.0.0.%d", i+1)),
		}
	}

	// Give manager time to process
	time.Sleep(200 * time.Millisecond)

	// We should be able to drain at least one transition (the buffer holds 1)
	select {
	case tr := <-m.Transitions():
		if !tr.HasChanged("ipv4") {
			t.Error("expected ipv4 in changed fields")
		}
	case <-time.After(time.Second):
		t.Fatal("expected at least one transition")
	}
}
