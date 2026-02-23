package state

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// RingBuffer tests

func TestRingBufferPushAndItems(t *testing.T) {
	rb := NewRingBuffer[int](5)

	rb.Push(1)
	rb.Push(2)
	rb.Push(3)

	items := rb.Items()
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}
	if items[0] != 1 || items[1] != 2 || items[2] != 3 {
		t.Errorf("Expected [1,2,3], got %v", items)
	}
}

func TestRingBufferOverflow(t *testing.T) {
	rb := NewRingBuffer[int](3)

	rb.Push(1)
	rb.Push(2)
	rb.Push(3)
	rb.Push(4) // Overwrites 1
	rb.Push(5) // Overwrites 2

	items := rb.Items()
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}
	if items[0] != 3 || items[1] != 4 || items[2] != 5 {
		t.Errorf("Expected [3,4,5], got %v", items)
	}
}

func TestRingBufferLen(t *testing.T) {
	rb := NewRingBuffer[string](5)

	if rb.Len() != 0 {
		t.Errorf("Expected Len()=0 for empty buffer, got %d", rb.Len())
	}

	rb.Push("a")
	rb.Push("b")
	if rb.Len() != 2 {
		t.Errorf("Expected Len()=2, got %d", rb.Len())
	}

	// Fill past capacity
	rb.Push("c")
	rb.Push("d")
	rb.Push("e")
	rb.Push("f") // Overflow
	if rb.Len() != 5 {
		t.Errorf("Expected Len()=5 (capped at capacity), got %d", rb.Len())
	}
}

func TestRingBufferClear(t *testing.T) {
	rb := NewRingBuffer[int](5)
	rb.Push(1)
	rb.Push(2)

	rb.Clear()

	if rb.Len() != 0 {
		t.Errorf("Expected Len()=0 after Clear(), got %d", rb.Len())
	}

	items := rb.Items()
	if items != nil {
		t.Errorf("Expected nil items after Clear(), got %v", items)
	}
}

func TestRingBufferEmptyItems(t *testing.T) {
	rb := NewRingBuffer[int](5)
	items := rb.Items()
	if items != nil {
		t.Errorf("Expected nil for empty buffer, got %v", items)
	}
}

// LogStreamer tests

func TestLogStreamerEmitAndSubscribe(t *testing.T) {
	ls := NewLogStreamer(100)

	id, ch := ls.Subscribe(false)
	defer ls.Unsubscribe(id)

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     LogInfo,
		Category:  CategoryState,
		Message:   "test message",
	}

	ls.Emit(entry)

	select {
	case got := <-ch:
		if got.Message != "test message" {
			t.Errorf("Expected message %q, got %q", "test message", got.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for emitted entry")
	}
}

func TestLogStreamerSubscribeWithReplay(t *testing.T) {
	ls := NewLogStreamer(100)

	// Emit some entries before subscribing
	for i := 0; i < 5; i++ {
		ls.Emit(LogEntry{
			Timestamp: time.Now(),
			Level:     LogInfo,
			Category:  CategorySystem,
			Message:   "history entry",
		})
	}

	// Subscribe with replay
	id, ch := ls.Subscribe(true)
	defer ls.Unsubscribe(id)

	// Should receive all 5 historical entries
	for i := 0; i < 5; i++ {
		select {
		case got := <-ch:
			if got.Message != "history entry" {
				t.Errorf("Expected %q, got %q", "history entry", got.Message)
			}
		case <-time.After(time.Second):
			t.Fatalf("Timed out waiting for history entry %d", i)
		}
	}
}

func TestLogStreamerSubscribeWithHistoryLimited(t *testing.T) {
	ls := NewLogStreamer(100)

	// Emit 10 entries
	for i := 0; i < 10; i++ {
		ls.Emit(LogEntry{
			Timestamp: time.Now(),
			Level:     LogInfo,
			Message:   "entry",
		})
	}

	// Subscribe with only last 3 entries
	id, ch := ls.SubscribeWithHistory(true, 3)
	defer ls.Unsubscribe(id)

	// Should get exactly 3 entries
	received := 0
	for {
		select {
		case <-ch:
			received++
		case <-time.After(100 * time.Millisecond):
			if received != 3 {
				t.Errorf("Expected 3 history entries, got %d", received)
			}
			return
		}
	}
}

func TestLogStreamerUnsubscribe(t *testing.T) {
	ls := NewLogStreamer(100)

	id, ch := ls.Subscribe(false)

	if ls.ClientCount() != 1 {
		t.Errorf("Expected 1 client, got %d", ls.ClientCount())
	}

	ls.Unsubscribe(id)

	if ls.ClientCount() != 0 {
		t.Errorf("Expected 0 clients after unsubscribe, got %d", ls.ClientCount())
	}

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Error("Expected channel to be closed after unsubscribe")
	}
}

func TestLogStreamerClientCount(t *testing.T) {
	ls := NewLogStreamer(100)

	if ls.ClientCount() != 0 {
		t.Errorf("Expected 0 clients initially, got %d", ls.ClientCount())
	}

	id1, _ := ls.Subscribe(false)
	id2, _ := ls.Subscribe(false)

	if ls.ClientCount() != 2 {
		t.Errorf("Expected 2 clients, got %d", ls.ClientCount())
	}

	ls.Unsubscribe(id1)
	ls.Unsubscribe(id2)

	if ls.ClientCount() != 0 {
		t.Errorf("Expected 0 clients after unsubscribing all, got %d", ls.ClientCount())
	}
}

func TestLogStreamerNoReplay(t *testing.T) {
	ls := NewLogStreamer(100)

	// Emit entries before subscribing
	ls.Emit(LogEntry{Message: "old"})
	ls.Emit(LogEntry{Message: "old"})

	// Subscribe without replay
	id, ch := ls.Subscribe(false)
	defer ls.Unsubscribe(id)

	// Emit a new entry
	ls.Emit(LogEntry{Message: "new"})

	select {
	case got := <-ch:
		if got.Message != "new" {
			t.Errorf("Expected %q, got %q", "new", got.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for entry")
	}
}

// LogRenderer tests

func TestLogRendererRenderSeparator(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true) // noColor=true for testable output

	r.RenderSeparator("Test Title")

	got := buf.String()
	if got != "--- Test Title ---\n" {
		t.Errorf("Expected %q, got %q", "--- Test Title ---\n", got)
	}
}

func TestLogRendererRenderSensor(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogInfo,
		Category:  CategorySensor,
		Sensor: &SensorLogData{
			Name:   "tcp",
			Online: boolPtr(true),
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "tcp") {
		t.Errorf("Expected output to contain sensor name, got %q", got)
	}
	if !strings.Contains(got, "online=true") {
		t.Errorf("Expected output to contain online=true, got %q", got)
	}
}

func TestLogRendererRenderTransition(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogInfo,
		Category:  CategoryState,
		Transition: &TransitionLogData{
			Field:  "context",
			From:   "home",
			To:     "office",
			Source: "rule match",
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "context") {
		t.Errorf("Expected output to contain field name, got %q", got)
	}
	if !strings.Contains(got, "home") || !strings.Contains(got, "office") {
		t.Errorf("Expected output to contain from/to values, got %q", got)
	}
}

func TestLogRendererRenderEffect(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogDebug,
		Category:  CategoryEffect,
		Effect: &EffectLogData{
			Name:    "env_write",
			Target:  "/tmp/test.env",
			Success: true,
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "env_write") {
		t.Errorf("Expected output to contain effect name, got %q", got)
	}
	if !strings.Contains(got, "ok") {
		t.Errorf("Expected output to contain 'ok' for success, got %q", got)
	}
}

func TestLogRendererRenderHook(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogInfo,
		Category:  CategoryHook,
		Hook: &HookLogData{
			Type:       "enter",
			Target:     "home",
			TargetType: "location",
			Command:    "echo hello",
			Success:    true,
			Duration:   150 * time.Millisecond,
			Output:     "hello\n",
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "enter") {
		t.Errorf("Expected output to contain hook type, got %q", got)
	}
	if !strings.Contains(got, "home") {
		t.Errorf("Expected output to contain target name, got %q", got)
	}
	if !strings.Contains(got, "cmd: echo hello") {
		t.Errorf("Expected output to contain command, got %q", got)
	}
	if !strings.Contains(got, "| hello") {
		t.Errorf("Expected output to contain hook output, got %q", got)
	}
}

func TestLogRendererRenderSystem(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogInfo,
		Category:  CategorySystem,
		Message:   "config loaded",
		System: &SystemLogData{
			Event:   "daemon_start",
			Details: "PID 12345",
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "daemon_start") {
		t.Errorf("Expected output to contain event, got %q", got)
	}
	if !strings.Contains(got, "PID 12345") {
		t.Errorf("Expected output to contain details, got %q", got)
	}
}

func TestLogRendererRenderSystemNoDetails(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogInfo,
		Category:  CategorySystem,
		Message:   "fallback message",
		System: &SystemLogData{
			Event: "config_reload",
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "config_reload") {
		t.Errorf("Expected output to contain event, got %q", got)
	}
	if !strings.Contains(got, "fallback message") {
		t.Errorf("Expected output to contain message as fallback, got %q", got)
	}
}

func TestLogRendererRenderSensorIP(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogInfo,
		Category:  CategorySensor,
		Sensor: &SensorLogData{
			Name:    "public_ipv4",
			IP:      "1.2.3.4",
			Latency: 50 * time.Millisecond,
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "1.2.3.4") {
		t.Errorf("Expected output to contain IP, got %q", got)
	}
	if !strings.Contains(got, "50ms") {
		t.Errorf("Expected output to contain latency, got %q", got)
	}
}

func TestLogRendererRenderSensorValue(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogInfo,
		Category:  CategorySensor,
		Sensor: &SensorLogData{
			Name:  "env:HOSTNAME",
			Value: "my-laptop",
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "my-laptop") {
		t.Errorf("Expected output to contain sensor value, got %q", got)
	}
}

func TestLogRendererRenderSensorError(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogError,
		Category:  CategorySensor,
		Sensor: &SensorLogData{
			Name:  "tcp",
			Error: "connection refused",
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "connection refused") {
		t.Errorf("Expected output to contain error, got %q", got)
	}
}

func TestLogRendererRenderEffectFailed(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogError,
		Category:  CategoryEffect,
		Effect: &EffectLogData{
			Name:     "env_write",
			Target:   "/tmp/test.env",
			Success:  false,
			Duration: 10 * time.Millisecond,
			Error:    "permission denied",
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "failed") {
		t.Errorf("Expected output to contain 'failed', got %q", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Errorf("Expected output to contain error message, got %q", got)
	}
	if !strings.Contains(got, "10ms") {
		t.Errorf("Expected output to contain duration, got %q", got)
	}
}

func TestLogRendererRenderHookFailed(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogError,
		Category:  CategoryHook,
		Hook: &HookLogData{
			Type:       "leave",
			Target:     "office",
			TargetType: "context",
			Command:    "cleanup.sh",
			Success:    false,
			Error:      "exit code 1",
		},
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "leave") {
		t.Errorf("Expected output to contain hook type, got %q", got)
	}
	if !strings.Contains(got, "failed") {
		t.Errorf("Expected output to contain 'failed', got %q", got)
	}
	if !strings.Contains(got, "exit code 1") {
		t.Errorf("Expected output to contain error, got %q", got)
	}
}

func TestLogRendererRenderSeparatorWithColor(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, false) // noColor=false

	r.RenderSeparator("Colored")

	got := buf.String()
	// Should contain ANSI escape codes
	if !strings.Contains(got, "\033[90m") {
		t.Errorf("Expected ANSI color codes in output, got %q", got)
	}
}

func TestLogRendererRenderPlainMessage(t *testing.T) {
	var buf bytes.Buffer
	r := NewLogRenderer(&buf, true)

	entry := LogEntry{
		Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     LogWarn,
		Category:  CategorySystem,
		Message:   "something happened",
	}

	r.Render(entry)

	got := buf.String()
	if !strings.Contains(got, "something happened") {
		t.Errorf("Expected output to contain message, got %q", got)
	}
}
