package daemon

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestHandleWrapperConnection_NormalOutput(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "test-comp",
		TunnelAlias: "test-tunnel",
		output:      broadcaster,
		ctx:         ctx,
		cancel:      cancel,
	}

	client, server := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.handleWrapperConnection(proc, server)
	}()

	// Subscribe to see broadcast output
	ch := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(ch)

	// Write normal output
	_, err := client.Write([]byte("hello world\n"))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	select {
	case msg := <-ch:
		if msg != "hello world\n" {
			t.Errorf("expected 'hello world\\n', got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for broadcast message")
	}

	client.Close()
	<-done
}

func TestHandleWrapperConnection_HistoryReplay(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	proc := &CompanionProcess{
		Name:        "test-comp",
		TunnelAlias: "test-tunnel",
		output:      broadcaster,
		ctx:         ctx,
		cancel:      cancel,
	}

	client, server := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.handleWrapperConnection(proc, server)
	}()

	// Subscribe to see broadcast output
	ch := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(ch)

	// Send history replay sequence
	_, err := client.Write([]byte("HISTORY_START\nhistory line 1\nhistory line 2\nHISTORY_END\n"))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// History lines should NOT appear on the broadcast channel
	// but should be in the history buffer
	// Send a normal line after to verify broadcast still works
	_, err = client.Write([]byte("normal line\n"))
	if err != nil {
		t.Fatalf("failed to write normal line: %v", err)
	}

	select {
	case msg := <-ch:
		if msg != "normal line\n" {
			t.Errorf("expected 'normal line\\n' (history lines shouldn't be broadcast), got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for broadcast message")
	}

	// Verify history was added to the broadcaster's buffer
	subCh, history := broadcaster.SubscribeWithHistory(10)
	defer broadcaster.Unsubscribe(subCh)

	foundHistoryLine := false
	for _, line := range history {
		if line == "history line 1\n" {
			foundHistoryLine = true
			break
		}
	}
	if !foundHistoryLine {
		t.Errorf("expected 'history line 1' in history buffer, got %v", history)
	}

	client.Close()
	<-done
}

func TestHandleWrapperConnection_CtxCancelled(t *testing.T) {
	quietLogger(t)

	cm := NewCompanionManager()
	broadcaster := NewLogBroadcaster(100)
	ctx, cancel := context.WithCancel(context.Background())

	proc := &CompanionProcess{
		Name:        "test-comp",
		TunnelAlias: "test-tunnel",
		output:      broadcaster,
		ctx:         ctx,
		cancel:      cancel,
	}

	_, server := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.handleWrapperConnection(proc, server)
	}()

	// Cancel context should cause handler to return
	cancel()

	select {
	case <-done:
		// Success - handler returned
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handler to return after ctx cancel")
	}
}
