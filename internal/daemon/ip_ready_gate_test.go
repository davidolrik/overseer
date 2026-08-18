package daemon

import (
	"bufio"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/core"
)

func TestWaitForPublicIPWith_ReturnsImmediatelyWhenReady(t *testing.T) {
	quietLogger(t)
	d := &Daemon{}

	notified := false
	d.waitForPublicIPWith(func() bool { return true }, time.Second, time.Millisecond, func() { notified = true })

	if notified {
		t.Error("notify should not be called when the IP is already known")
	}
}

func TestWaitForPublicIPWith_WaitsThenProceeds(t *testing.T) {
	quietLogger(t)
	d := &Daemon{}

	var checks int32
	ready := func() bool { return atomic.AddInt32(&checks, 1) > 3 } // false first 3 calls
	var notifyCount int32

	start := time.Now()
	d.waitForPublicIPWith(ready, time.Second, time.Millisecond, func() { atomic.AddInt32(&notifyCount, 1) })
	elapsed := time.Since(start)

	if got := atomic.LoadInt32(&notifyCount); got != 1 {
		t.Errorf("expected notify called exactly once, got %d", got)
	}
	if elapsed >= time.Second {
		t.Errorf("expected to return well before the timeout, took %v", elapsed)
	}
}

func TestWaitForPublicIPWith_ProceedsAfterTimeout(t *testing.T) {
	quietLogger(t)
	d := &Daemon{}

	var notifyCount int32
	start := time.Now()
	d.waitForPublicIPWith(func() bool { return false }, 60*time.Millisecond, time.Millisecond, func() { atomic.AddInt32(&notifyCount, 1) })
	elapsed := time.Since(start)

	if got := atomic.LoadInt32(&notifyCount); got != 1 {
		t.Errorf("expected notify called exactly once, got %d", got)
	}
	if elapsed < 60*time.Millisecond {
		t.Errorf("expected to wait for the full timeout, took %v", elapsed)
	}
}

// A manual connect that arrives before the public IP is known must stream a
// waiting message before it tries to connect. Uses a trailing-dot alias so
// the connection attempt fails fast once the (never-satisfied) wait times out.
func TestHandleConnection_SSHConnect_WaitsForPublicIP(t *testing.T) {
	quietLogger(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()
	// Public IP never becomes known; keep the timeout short so the test is quick.
	d.ipReady = func() bool { return false }
	d.publicIPWaitTimeout = 150 * time.Millisecond

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("SSH_CONNECT waits-for-ip.\n"))

	reader := bufio.NewReader(clientConn)
	sawWaiting := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "Waiting for network context") {
			sawWaiting = true
			break
		}
	}

	if !sawWaiting {
		t.Error("expected a 'Waiting for network context' message before connecting")
	}

	clientConn.Close()
	<-done
}

// When the IP is already known, no waiting message is streamed.
func TestHandleConnection_SSHConnect_NoWaitWhenReady(t *testing.T) {
	quietLogger(t)

	old := stateOrchestrator
	t.Cleanup(func() { stateOrchestrator = old })
	stateOrchestrator = nil

	oldConfig := core.Config
	t.Cleanup(func() { core.Config = oldConfig })
	core.Config = &core.Configuration{
		Companion: core.CompanionSettings{HistorySize: 50},
		Tunnels:   map[string]*core.TunnelConfig{},
		SSH:       core.SSHConfig{},
	}

	d := New()
	d.ipReady = func() bool { return true }

	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConnection(serverConn)
	}()

	clientConn.Write([]byte("SSH_CONNECT no-wait-alias.\n"))

	reader := bufio.NewReader(clientConn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "Waiting for network context") {
			t.Error("did not expect a waiting message when the IP is already known")
			break
		}
	}

	clientConn.Close()
	<-done
}
