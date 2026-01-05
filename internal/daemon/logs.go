package daemon

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/lmittmann/tint"
	"overseer.olrik.dev/internal/awareness/state"
)

// LogBroadcaster manages streaming logs to multiple clients
type LogBroadcaster struct {
	clients map[chan string]bool
	mu      sync.RWMutex
}

// NewLogBroadcaster creates a new log broadcaster
func NewLogBroadcaster() *LogBroadcaster {
	return &LogBroadcaster{
		clients: make(map[chan string]bool),
	}
}

// Subscribe adds a new client to receive log broadcasts
func (lb *LogBroadcaster) Subscribe() chan string {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	ch := make(chan string, 100) // Buffer to prevent blocking
	lb.clients[ch] = true
	return ch
}

// Unsubscribe removes a client from receiving broadcasts
func (lb *LogBroadcaster) Unsubscribe(ch chan string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	delete(lb.clients, ch)
	close(ch)
}

// Broadcast sends a log message to all subscribed clients
func (lb *LogBroadcaster) Broadcast(message string) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	for ch := range lb.clients {
		select {
		case ch <- message:
		default:
			// Channel buffer full, skip this client to prevent blocking
		}
	}
}

// LogWriter is an io.Writer that broadcasts log messages
type LogWriter struct {
	broadcaster *LogBroadcaster
}

func (lw *LogWriter) Write(p []byte) (n int, err error) {
	message := string(p)
	lw.broadcaster.Broadcast(message)
	return len(p), nil
}

// setupLogging configures the daemon's logger to broadcast to connected clients
func (d *Daemon) setupLogging() {
	// Create a multi-writer that writes to both stderr and the broadcaster
	logWriter := &LogWriter{broadcaster: d.logBroadcast}
	multiWriter := io.MultiWriter(os.Stderr, logWriter)

	// Set up tint handler with the multi-writer
	handler := tint.NewHandler(multiWriter, &tint.Options{
		Level:      slog.LevelDebug,
		TimeFormat: time.DateTime,
	})

	// Set as the default logger
	slog.SetDefault(slog.New(handler))
}

// handleLogs streams daemon logs to the client until they disconnect
func (d *Daemon) handleLogs(conn net.Conn) {
	defer conn.Close()

	// Use handleLogsWithState which includes both slog and state events
	if stateOrchestrator != nil {
		d.handleLogsWithState(conn)
		return
	}

	// Fallback to just slog if state orchestrator not initialized
	logChan := d.logBroadcast.Subscribe()
	defer d.logBroadcast.Unsubscribe(logChan)

	initialMsg := "Connected to overseer daemon logs. Press Ctrl+C to exit.\n"
	if _, err := conn.Write([]byte(initialMsg)); err != nil {
		slog.Warn(fmt.Sprintf("Failed to send initial message to logs client: %v", err))
		return
	}

	done := make(chan bool)
	go func() {
		reader := bufio.NewReader(conn)
		io.Copy(io.Discard, reader)
		done <- true
	}()

	for {
		select {
		case logMsg, ok := <-logChan:
			if !ok {
				return
			}
			if _, err := conn.Write([]byte(logMsg)); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// handleAttach streams raw slog output to the client (same as daemon stderr)
func (d *Daemon) handleAttach(conn net.Conn) {
	defer conn.Close()

	// Subscribe to slog broadcasts
	logChan := d.logBroadcast.Subscribe()
	defer d.logBroadcast.Unsubscribe(logChan)

	// Send initial message
	initialMsg := "Attached to overseer daemon. Press Ctrl+C to detach.\n\n"
	if _, err := conn.Write([]byte(initialMsg)); err != nil {
		return
	}

	// Detect when client disconnects
	done := make(chan bool)
	go func() {
		reader := bufio.NewReader(conn)
		io.Copy(io.Discard, reader)
		done <- true
	}()

	// Stream logs
	for {
		select {
		case logMsg, ok := <-logChan:
			if !ok {
				return
			}
			if _, err := conn.Write([]byte(logMsg)); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// handleLogsWithState streams both slog and structured state logs
func (d *Daemon) handleLogsWithState(conn net.Conn) {
	// Subscribe to state log channel (which includes all events)
	stateID, stateChan := stateOrchestrator.SubscribeLogs(true)
	defer stateOrchestrator.UnsubscribeLogs(stateID)

	// Send initial message
	initialMsg := "Connected to overseer daemon logs. Press Ctrl+C to exit.\n"
	if _, err := conn.Write([]byte(initialMsg)); err != nil {
		slog.Warn(fmt.Sprintf("Failed to send initial message to logs client: %v", err))
		return
	}

	// Create renderer for formatting log entries
	renderer := state.NewLogRenderer(conn, false)

	// Render separator for history replay
	renderer.RenderSeparator("Recent History")

	// Create a reader for the connection to detect when client disconnects
	done := make(chan bool)
	go func() {
		reader := bufio.NewReader(conn)
		io.Copy(io.Discard, reader)
		done <- true
	}()

	// Stream logs
	for {
		select {
		case entry, ok := <-stateChan:
			if !ok {
				return
			}
			renderer.Render(entry)

		case <-done:
			return
		}
	}
}

