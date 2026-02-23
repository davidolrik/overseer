package daemon

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLogBroadcasterCreation(t *testing.T) {
	lb := NewLogBroadcaster(100)
	if lb == nil {
		t.Fatal("Expected non-nil LogBroadcaster")
	}
}

func TestLogBroadcasterDefaultHistorySize(t *testing.T) {
	// Zero or negative should default to 1000
	lb := NewLogBroadcaster(0)
	if lb.maxHist != 1000 {
		t.Errorf("Expected default maxHist=1000, got %d", lb.maxHist)
	}

	lb = NewLogBroadcaster(-1)
	if lb.maxHist != 1000 {
		t.Errorf("Expected default maxHist=1000 for negative value, got %d", lb.maxHist)
	}
}

func TestLogBroadcasterSubscribeAndBroadcast(t *testing.T) {
	lb := NewLogBroadcaster(100)

	ch := lb.Subscribe()
	defer lb.Unsubscribe(ch)

	lb.Broadcast("hello")

	select {
	case msg := <-ch:
		if msg != "hello" {
			t.Errorf("Expected %q, got %q", "hello", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for broadcast message")
	}
}

func TestLogBroadcasterMultipleSubscribers(t *testing.T) {
	lb := NewLogBroadcaster(100)

	ch1 := lb.Subscribe()
	defer lb.Unsubscribe(ch1)
	ch2 := lb.Subscribe()
	defer lb.Unsubscribe(ch2)

	lb.Broadcast("test")

	for i, ch := range []chan string{ch1, ch2} {
		select {
		case msg := <-ch:
			if msg != "test" {
				t.Errorf("Subscriber %d: expected %q, got %q", i, "test", msg)
			}
		case <-time.After(time.Second):
			t.Fatalf("Subscriber %d: timed out", i)
		}
	}
}

func TestLogBroadcasterUnsubscribe(t *testing.T) {
	lb := NewLogBroadcaster(100)

	ch := lb.Subscribe()
	lb.Unsubscribe(ch)

	// Channel should be closed
	_, ok := <-ch
	if ok {
		t.Error("Expected channel to be closed after unsubscribe")
	}
}

func TestLogBroadcasterSubscribeWithHistory(t *testing.T) {
	lb := NewLogBroadcaster(100)

	lb.Broadcast("msg1")
	lb.Broadcast("msg2")
	lb.Broadcast("msg3")

	ch, history := lb.SubscribeWithHistory(2)
	defer lb.Unsubscribe(ch)

	if len(history) != 2 {
		t.Fatalf("Expected 2 history entries, got %d", len(history))
	}
	if history[0] != "msg2" {
		t.Errorf("Expected first history entry %q, got %q", "msg2", history[0])
	}
	if history[1] != "msg3" {
		t.Errorf("Expected second history entry %q, got %q", "msg3", history[1])
	}
}

func TestLogBroadcasterSubscribeWithHistoryMoreThanAvailable(t *testing.T) {
	lb := NewLogBroadcaster(100)

	lb.Broadcast("only one")

	ch, history := lb.SubscribeWithHistory(10)
	defer lb.Unsubscribe(ch)

	if len(history) != 1 {
		t.Fatalf("Expected 1 history entry, got %d", len(history))
	}
	if history[0] != "only one" {
		t.Errorf("Expected %q, got %q", "only one", history[0])
	}
}

func TestLogBroadcasterHistoryRingBuffer(t *testing.T) {
	lb := NewLogBroadcaster(3) // Only 3 entries max

	lb.Broadcast("a")
	lb.Broadcast("b")
	lb.Broadcast("c")
	lb.Broadcast("d") // Pushes out "a"

	ch, history := lb.SubscribeWithHistory(10)
	defer lb.Unsubscribe(ch)

	if len(history) != 3 {
		t.Fatalf("Expected 3 history entries, got %d", len(history))
	}
	if history[0] != "b" || history[1] != "c" || history[2] != "d" {
		t.Errorf("Expected [b,c,d], got %v", history)
	}
}

func TestLogBroadcasterAddToHistory(t *testing.T) {
	lb := NewLogBroadcaster(100)

	ch := lb.Subscribe()
	defer lb.Unsubscribe(ch)

	// AddToHistory should NOT broadcast to subscribers
	lb.AddToHistory("silent message")

	select {
	case msg := <-ch:
		t.Errorf("Expected no broadcast from AddToHistory, got %q", msg)
	case <-time.After(50 * time.Millisecond):
		// Expected: no message
	}

	// But the message should be in history
	_, history := lb.SubscribeWithHistory(10)
	if len(history) != 1 || history[0] != "silent message" {
		t.Errorf("Expected history to contain 'silent message', got %v", history)
	}
}

func TestLogBroadcasterClearHistory(t *testing.T) {
	lb := NewLogBroadcaster(100)

	lb.Broadcast("msg1")
	lb.Broadcast("msg2")

	lb.ClearHistory()

	ch, history := lb.SubscribeWithHistory(10)
	defer lb.Unsubscribe(ch)

	if len(history) != 0 {
		t.Errorf("Expected empty history after clear, got %d entries", len(history))
	}
}

func TestLogWriterWrite(t *testing.T) {
	lb := NewLogBroadcaster(100)
	lw := &LogWriter{broadcaster: lb}

	ch := lb.Subscribe()
	defer lb.Unsubscribe(ch)

	msg := "test log message\n"
	n, err := lw.Write([]byte(msg))
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	if n != len(msg) {
		t.Errorf("Write() returned %d, want %d", n, len(msg))
	}

	select {
	case got := <-ch:
		if got != msg {
			t.Errorf("Expected broadcast %q, got %q", msg, got)
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for broadcast from LogWriter")
	}
}

func TestLogBroadcasterAddToHistoryRingBuffer(t *testing.T) {
	lb := NewLogBroadcaster(3) // max 3 entries

	lb.AddToHistory("a")
	lb.AddToHistory("b")
	lb.AddToHistory("c")
	lb.AddToHistory("d") // Pushes out "a"

	ch, history := lb.SubscribeWithHistory(10)
	defer lb.Unsubscribe(ch)

	if len(history) != 3 {
		t.Fatalf("Expected 3 history entries, got %d", len(history))
	}
	if history[0] != "b" || history[1] != "c" || history[2] != "d" {
		t.Errorf("Expected [b,c,d], got %v", history)
	}
}

func TestLogBroadcasterBroadcastFullChannelSkips(t *testing.T) {
	lb := NewLogBroadcaster(100)

	ch := lb.Subscribe()
	defer lb.Unsubscribe(ch)

	// Fill the channel buffer (100 entries)
	for i := 0; i < 100; i++ {
		lb.Broadcast("fill")
	}

	// This should not block even though channel is full
	done := make(chan bool)
	go func() {
		lb.Broadcast("overflow")
		done <- true
	}()

	select {
	case <-done:
		// Expected: broadcast completed without blocking
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked on full channel - should skip")
	}
}

func TestLogBroadcaster_ConcurrentBroadcast(t *testing.T) {
	lb := NewLogBroadcaster(100)

	ch := lb.Subscribe()
	defer lb.Unsubscribe(ch)

	// Multiple goroutines broadcasting simultaneously should not race
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				lb.Broadcast(fmt.Sprintf("goroutine-%d-msg-%d", n, j))
			}
		}(i)
	}

	// Drain the channel to avoid blocking
	drainDone := make(chan struct{})
	go func() {
		count := 0
		for range ch {
			count++
			if count >= 100 {
				break
			}
		}
		close(drainDone)
	}()

	wg.Wait()

	select {
	case <-drainDone:
		// All messages received
	case <-time.After(5 * time.Second):
		// Some may have been dropped due to full channel, that's OK
	}
}

func TestLogBroadcaster_UnsubscribeCleanup(t *testing.T) {
	lb := NewLogBroadcaster(100)

	ch := lb.Subscribe()

	// Verify channel is open
	lb.Broadcast("test")
	select {
	case msg := <-ch:
		if msg != "test" {
			t.Errorf("expected 'test', got %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}

	lb.Unsubscribe(ch)

	// Channel should be closed after unsubscribe
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after unsubscribe")
	}

	// Broadcasting after unsubscribe should not panic
	lb.Broadcast("after unsubscribe")
}

func TestLogBroadcaster_ConcurrentSubscribeUnsubscribe(t *testing.T) {
	lb := NewLogBroadcaster(100)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := lb.Subscribe()
			lb.Broadcast("test")
			time.Sleep(10 * time.Millisecond)
			lb.Unsubscribe(ch)
		}()
	}

	wg.Wait()
}
