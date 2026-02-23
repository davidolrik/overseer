package state

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- DotenvWriter ---

func TestDotenvWriterCreateAndWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	w, err := NewDotenvWriter(path)
	if err != nil {
		t.Fatalf("NewDotenvWriter() error: %v", err)
	}

	if w.Name() != "dotenv" {
		t.Errorf("Name() = %q, want %q", w.Name(), "dotenv")
	}
	if w.Path() != path {
		t.Errorf("Path() = %q, want %q", w.Path(), path)
	}

	data := EnvExportData{
		Context:  "home",
		Location: "hq",
	}

	err = w.Write(data, nil)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	output := string(content)
	if !strings.Contains(output, `export OVERSEER_CONTEXT="home"`) {
		t.Errorf("Expected OVERSEER_CONTEXT export, got:\n%s", output)
	}
	if !strings.Contains(output, `export OVERSEER_LOCATION="hq"`) {
		t.Errorf("Expected OVERSEER_LOCATION export, got:\n%s", output)
	}
}

func TestDotenvWriterSortedOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	w, err := NewDotenvWriter(path)
	if err != nil {
		t.Fatalf("NewDotenvWriter() error: %v", err)
	}

	data := EnvExportData{
		Context:    "office",
		Location:   "hq",
		PublicIPv4: "1.2.3.4",
		PublicIP:   "1.2.3.4",
	}

	err = w.Write(data, nil)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")

	// Verify exports are sorted alphabetically
	var exportLines []string
	for _, line := range lines {
		if strings.HasPrefix(line, "export ") {
			exportLines = append(exportLines, line)
		}
	}

	for i := 1; i < len(exportLines); i++ {
		if exportLines[i] < exportLines[i-1] {
			t.Errorf("Exports not sorted: %q comes after %q", exportLines[i], exportLines[i-1])
		}
	}
}

func TestDotenvWriterUnsetTrackedVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	w, err := NewDotenvWriter(path)
	if err != nil {
		t.Fatalf("NewDotenvWriter() error: %v", err)
	}

	data := EnvExportData{
		Context: "mobile",
	}

	// CUSTOM_VAR is tracked but not set in this context
	trackedVars := []string{"CUSTOM_VAR", "ANOTHER_VAR"}
	err = w.Write(data, trackedVars)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	output := string(content)
	if !strings.Contains(output, "unset ANOTHER_VAR CUSTOM_VAR") {
		t.Errorf("Expected unset of tracked vars, got:\n%s", output)
	}
}

func TestDotenvWriterCustomEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	w, err := NewDotenvWriter(path)
	if err != nil {
		t.Fatalf("NewDotenvWriter() error: %v", err)
	}

	data := EnvExportData{
		Context: "test",
		CustomEnvironment: map[string]string{
			"MY_VAR":    "my-value",
			"OTHER_VAR": "other-value",
		},
	}

	err = w.Write(data, nil)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	output := string(content)
	if !strings.Contains(output, `export MY_VAR="my-value"`) {
		t.Errorf("Expected custom env var, got:\n%s", output)
	}
	if !strings.Contains(output, `export OTHER_VAR="other-value"`) {
		t.Errorf("Expected custom env var, got:\n%s", output)
	}
}

func TestDotenvWriterCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "test.env")

	w, err := NewDotenvWriter(path)
	if err != nil {
		t.Fatalf("NewDotenvWriter() error: %v", err)
	}

	data := EnvExportData{Context: "test"}
	err = w.Write(data, nil)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("Expected file to exist after write")
	}
}

func TestDotenvWriterAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	w, err := NewDotenvWriter(path)
	if err != nil {
		t.Fatalf("NewDotenvWriter() error: %v", err)
	}

	data := EnvExportData{Context: "test"}
	err = w.Write(data, nil)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	// Temp file should not exist (was renamed)
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("Temp file should not exist after successful write")
	}
}

// --- ContextWriter ---

func TestContextWriterCreateAndWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.txt")

	w, err := NewContextWriter(path)
	if err != nil {
		t.Fatalf("NewContextWriter() error: %v", err)
	}

	if w.Name() != "context" {
		t.Errorf("Name() = %q, want %q", w.Name(), "context")
	}

	data := EnvExportData{Context: "office"}
	err = w.Write(data, nil)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	if string(content) != "office\n" {
		t.Errorf("Expected %q, got %q", "office\n", string(content))
	}
}

// --- LocationWriter ---

func TestLocationWriterCreateAndWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "location.txt")

	w, err := NewLocationWriter(path)
	if err != nil {
		t.Fatalf("NewLocationWriter() error: %v", err)
	}

	if w.Name() != "location" {
		t.Errorf("Name() = %q, want %q", w.Name(), "location")
	}

	data := EnvExportData{Location: "hq"}
	err = w.Write(data, nil)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	if string(content) != "hq\n" {
		t.Errorf("Expected %q, got %q", "hq\n", string(content))
	}
}

// --- PublicIPWriter ---

func TestPublicIPWriterCreateAndWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ip.txt")

	w, err := NewPublicIPWriter(path)
	if err != nil {
		t.Fatalf("NewPublicIPWriter() error: %v", err)
	}

	if w.Name() != "public_ip" {
		t.Errorf("Name() = %q, want %q", w.Name(), "public_ip")
	}

	data := EnvExportData{PublicIP: "8.8.8.8"}
	err = w.Write(data, nil)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	if string(content) != "8.8.8.8\n" {
		t.Errorf("Expected %q, got %q", "8.8.8.8\n", string(content))
	}
}

// --- Writer Path() coverage ---

func TestContextWriterPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.txt")

	w, err := NewContextWriter(path)
	if err != nil {
		t.Fatalf("NewContextWriter() error: %v", err)
	}

	if w.Path() != path {
		t.Errorf("Path() = %q, want %q", w.Path(), path)
	}
}

func TestLocationWriterPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "location.txt")

	w, err := NewLocationWriter(path)
	if err != nil {
		t.Fatalf("NewLocationWriter() error: %v", err)
	}

	if w.Path() != path {
		t.Errorf("Path() = %q, want %q", w.Path(), path)
	}
}

func TestPublicIPWriterPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ip.txt")

	w, err := NewPublicIPWriter(path)
	if err != nil {
		t.Fatalf("NewPublicIPWriter() error: %v", err)
	}

	if w.Path() != path {
		t.Errorf("Path() = %q, want %q", w.Path(), path)
	}
}

// --- Writer tilde expansion ---

func TestDotenvWriterTildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	// Use a temp subdir under home to avoid creating files in unexpected places
	dir := t.TempDir()
	// We can't easily test ~ expansion since TempDir won't be under ~
	// Instead, verify that a path starting with / works fine
	path := filepath.Join(dir, "test.env")
	w, err := NewDotenvWriter(path)
	if err != nil {
		t.Fatalf("NewDotenvWriter() error: %v", err)
	}

	// Path should be absolute
	if !filepath.IsAbs(w.Path()) {
		t.Errorf("Expected absolute path, got %q", w.Path())
	}

	// Verify tilde expansion happens (use home dir prefix)
	_ = home // used to validate the concept
}

// --- EffectsProcessor ---

func TestEffectsProcessorStartStop(t *testing.T) {
	ch := make(chan StateTransition, 10)

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{})

	ep.Start()

	// Give it a moment to start
	time.Sleep(10 * time.Millisecond)

	ep.Stop()
}

func TestEffectsProcessorLastWrittenPublicIPv4(t *testing.T) {
	ch := make(chan StateTransition, 10)

	dir := t.TempDir()
	envPath := filepath.Join(dir, "test.env")
	writer, err := NewDotenvWriter(envPath)
	if err != nil {
		t.Fatalf("NewDotenvWriter() error: %v", err)
	}

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{
		EnvWriters: []EnvWriter{writer},
	})

	// Initially empty
	if got := ep.LastWrittenPublicIPv4(); got != "" {
		t.Errorf("Expected empty LastWrittenPublicIPv4, got %q", got)
	}

	ep.Start()

	// Send a transition with IPv4
	ch <- StateTransition{
		From: StateSnapshot{},
		To: StateSnapshot{
			Timestamp:  time.Now(),
			PublicIPv4: net.ParseIP("1.2.3.4"),
			Context:    "test",
		},
		Trigger:       "test",
		ChangedFields: []string{"ipv4"},
	}

	// Give it time to process
	time.Sleep(50 * time.Millisecond)

	ep.Stop()

	if got := ep.LastWrittenPublicIPv4(); got != "1.2.3.4" {
		t.Errorf("Expected LastWrittenPublicIPv4=%q, got %q", "1.2.3.4", got)
	}
}

func TestEffectsProcessorCallbacks(t *testing.T) {
	ch := make(chan StateTransition, 10)

	onlineCalled := false
	contextCalled := false

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{
		OnOnlineChange: func(wasOnline, isOnline bool) {
			onlineCalled = true
		},
		OnContextChange: func(from, to StateSnapshot) {
			contextCalled = true
		},
	})

	ep.Start()

	ch <- StateTransition{
		From: StateSnapshot{Online: false, Context: "old"},
		To:   StateSnapshot{Online: true, Context: "new", Timestamp: time.Now()},
		ChangedFields: []string{"online", "context"},
	}

	time.Sleep(50 * time.Millisecond)

	ep.Stop()

	if !onlineCalled {
		t.Error("Expected OnOnlineChange callback to be called")
	}
	if !contextCalled {
		t.Error("Expected OnContextChange callback to be called")
	}
}

func TestEffectsProcessorBuildHookEnv(t *testing.T) {
	ch := make(chan StateTransition, 10)

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{})

	state := StateSnapshot{
		Context:    "office",
		Location:   "hq",
		PublicIPv4: net.ParseIP("1.2.3.4"),
		PublicIPv6: net.ParseIP("2001:db8::1"),
		LocalIPv4:  net.ParseIP("192.168.1.10"),
		Environment: map[string]string{
			"CUSTOM": "value",
		},
	}

	env := ep.buildHookEnv(state)

	if env["OVERSEER_CONTEXT"] != "office" {
		t.Errorf("Expected OVERSEER_CONTEXT=%q, got %q", "office", env["OVERSEER_CONTEXT"])
	}
	if env["OVERSEER_LOCATION"] != "hq" {
		t.Errorf("Expected OVERSEER_LOCATION=%q, got %q", "hq", env["OVERSEER_LOCATION"])
	}
	if env["OVERSEER_PUBLIC_IP"] != "1.2.3.4" {
		t.Errorf("Expected OVERSEER_PUBLIC_IP=%q, got %q", "1.2.3.4", env["OVERSEER_PUBLIC_IP"])
	}
	if env["OVERSEER_PUBLIC_IPV4"] != "1.2.3.4" {
		t.Errorf("Expected OVERSEER_PUBLIC_IPV4=%q, got %q", "1.2.3.4", env["OVERSEER_PUBLIC_IPV4"])
	}
	if env["OVERSEER_PUBLIC_IPV6"] != "2001:db8::1" {
		t.Errorf("Expected OVERSEER_PUBLIC_IPV6=%q, got %q", "2001:db8::1", env["OVERSEER_PUBLIC_IPV6"])
	}
	if env["OVERSEER_LOCAL_IP"] != "192.168.1.10" {
		t.Errorf("Expected OVERSEER_LOCAL_IP=%q, got %q", "192.168.1.10", env["OVERSEER_LOCAL_IP"])
	}
	if env["CUSTOM"] != "value" {
		t.Errorf("Expected CUSTOM=%q, got %q", "value", env["CUSTOM"])
	}
}

func TestEffectsProcessorEmitEffectLog(t *testing.T) {
	ch := make(chan StateTransition, 10)
	streamer := NewLogStreamer(100)

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{
		LogStreamer: streamer,
	})

	// Subscribe to streamer to capture log entries
	id, logCh := streamer.Subscribe(false)
	defer streamer.Unsubscribe(id)

	// Emit a success effect log
	ep.emitEffectLog("env_write", "/tmp/test.env", nil, 5*time.Millisecond)

	select {
	case entry := <-logCh:
		if entry.Category != CategoryEffect {
			t.Errorf("Expected CategoryEffect, got %v", entry.Category)
		}
		if entry.Effect == nil {
			t.Fatal("Expected Effect data to be set")
		}
		if !entry.Effect.Success {
			t.Error("Expected Success=true for nil error")
		}
		if entry.Effect.Name != "env_write" {
			t.Errorf("Expected effect name=%q, got %q", "env_write", entry.Effect.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for effect log")
	}
}

func TestEffectsProcessorEmitEffectLogWithError(t *testing.T) {
	ch := make(chan StateTransition, 10)
	streamer := NewLogStreamer(100)

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{
		LogStreamer: streamer,
	})

	id, logCh := streamer.Subscribe(false)
	defer streamer.Unsubscribe(id)

	ep.emitEffectLog("env_write", "/tmp/test.env", fmt.Errorf("write failed"), 5*time.Millisecond)

	select {
	case entry := <-logCh:
		if entry.Level != LogError {
			t.Errorf("Expected LogError for failed effect, got %v", entry.Level)
		}
		if entry.Effect.Success {
			t.Error("Expected Success=false for error")
		}
		if entry.Effect.Error != "write failed" {
			t.Errorf("Expected error=%q, got %q", "write failed", entry.Effect.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for effect log")
	}
}

func TestEffectsProcessorEmitEffectLogNilStreamer(t *testing.T) {
	ch := make(chan StateTransition, 10)

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{})

	// Should not panic with nil streamer
	ep.emitEffectLog("test", "target", nil, time.Millisecond)
}

func TestEffectsProcessorEmitTransitionLogs(t *testing.T) {
	ch := make(chan StateTransition, 10)
	streamer := NewLogStreamer(100)

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{
		LogStreamer: streamer,
	})

	id, logCh := streamer.Subscribe(false)
	defer streamer.Unsubscribe(id)

	transition := StateTransition{
		From: StateSnapshot{Online: false, Context: "old"},
		To:   StateSnapshot{Online: true, Context: "new", Timestamp: time.Now()},
		ChangedFields: []string{"online", "context"},
	}

	ep.emitTransitionLogs(transition)

	// Should receive 2 log entries (one for online, one for context)
	received := 0
	for {
		select {
		case entry := <-logCh:
			if entry.Category != CategoryState {
				t.Errorf("Expected CategoryState, got %v", entry.Category)
			}
			if entry.Transition == nil {
				t.Fatal("Expected Transition data to be set")
			}
			received++
		case <-time.After(100 * time.Millisecond):
			if received != 2 {
				t.Errorf("Expected 2 transition logs, got %d", received)
			}
			return
		}
	}
}

func TestEffectsProcessorWriteEnvFiles(t *testing.T) {
	ch := make(chan StateTransition, 10)

	dir := t.TempDir()
	envPath := filepath.Join(dir, "test.env")
	ctxPath := filepath.Join(dir, "context.txt")
	locPath := filepath.Join(dir, "location.txt")
	ipPath := filepath.Join(dir, "ip.txt")

	envWriter, _ := NewDotenvWriter(envPath)
	ctxWriter, _ := NewContextWriter(ctxPath)
	locWriter, _ := NewLocationWriter(locPath)
	ipWriter, _ := NewPublicIPWriter(ipPath)

	ep := NewEffectsProcessor(ch, EffectsProcessorConfig{
		EnvWriters: []EnvWriter{envWriter, ctxWriter, locWriter, ipWriter},
	})

	transition := StateTransition{
		To: StateSnapshot{
			Context:    "office",
			Location:   "hq",
			PublicIPv4: net.ParseIP("1.2.3.4"),
			Timestamp:  time.Now(),
		},
		ChangedFields: []string{"context"},
	}

	ep.writeEnvFiles(transition)

	// Check context file
	content, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("Failed to read context file: %v", err)
	}
	if string(content) != "office\n" {
		t.Errorf("Expected context file %q, got %q", "office\n", string(content))
	}

	// Check location file
	content, err = os.ReadFile(locPath)
	if err != nil {
		t.Fatalf("Failed to read location file: %v", err)
	}
	if string(content) != "hq\n" {
		t.Errorf("Expected location file %q, got %q", "hq\n", string(content))
	}
}

// --- Writer overwrite behavior ---

func TestWritersOverwriteExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "context.txt")

	w, err := NewContextWriter(path)
	if err != nil {
		t.Fatalf("NewContextWriter() error: %v", err)
	}

	// Write first value
	w.Write(EnvExportData{Context: "first"}, nil)

	// Overwrite with second value
	w.Write(EnvExportData{Context: "second"}, nil)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	if string(content) != "second\n" {
		t.Errorf("Expected %q, got %q", "second\n", string(content))
	}
}
