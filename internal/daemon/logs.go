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
	history []string // Ring buffer for recent messages
	maxHist int      // Maximum history size
	mu      sync.RWMutex
}

// NewLogBroadcaster creates a new log broadcaster with the specified history size
func NewLogBroadcaster(historySize int) *LogBroadcaster {
	if historySize <= 0 {
		historySize = 1000 // default
	}
	return &LogBroadcaster{
		clients: make(map[chan string]bool),
		history: make([]string, 0, historySize),
		maxHist: historySize,
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

// SubscribeWithHistory adds a new client and returns recent history
// The history slice is returned separately to avoid blocking the channel
func (lb *LogBroadcaster) SubscribeWithHistory(historyLines int) (chan string, []string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	ch := make(chan string, 100) // Buffer to prevent blocking
	lb.clients[ch] = true

	// Return the last N lines from history
	var history []string
	if historyLines > 0 && len(lb.history) > 0 {
		start := len(lb.history) - historyLines
		if start < 0 {
			start = 0
		}
		history = make([]string, len(lb.history)-start)
		copy(history, lb.history[start:])
	}

	return ch, history
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
	lb.mu.Lock()
	defer lb.mu.Unlock()

	// Add to history buffer
	if len(lb.history) >= lb.maxHist {
		// Remove oldest entry
		lb.history = lb.history[1:]
	}
	lb.history = append(lb.history, message)

	// Broadcast to all clients
	for ch := range lb.clients {
		select {
		case ch <- message:
		default:
			// Channel buffer full, skip this client to prevent blocking
		}
	}
}

// AddToHistory adds a message to history without broadcasting to subscribers
// Used for history replay from wrapper reconnection
func (lb *LogBroadcaster) AddToHistory(message string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if len(lb.history) >= lb.maxHist {
		lb.history = lb.history[1:]
	}
	lb.history = append(lb.history, message)
}

// ClearHistory clears the history buffer
func (lb *LogBroadcaster) ClearHistory() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.history = lb.history[:0]
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
	d.handleLogsWithHistory(conn, true, 20)
}

// handleLogsWithHistory streams daemon logs to the client with configurable history
func (d *Daemon) handleLogsWithHistory(conn net.Conn, showHistory bool, historyLines int) {
	defer conn.Close()

	// Use handleLogsWithState which includes both slog and state events
	if stateOrchestrator != nil {
		d.handleLogsWithStateAndHistory(conn, showHistory, historyLines)
		return
	}

	// Fallback to just slog if state orchestrator not initialized
	var logChan chan string
	var history []string
	if showHistory {
		logChan, history = d.logBroadcast.SubscribeWithHistory(historyLines)
	} else {
		logChan = d.logBroadcast.Subscribe()
	}
	defer d.logBroadcast.Unsubscribe(logChan)

	initialMsg := "Connected to overseer daemon logs. Press Ctrl+C to exit.\n"
	if _, err := conn.Write([]byte(initialMsg)); err != nil {
		slog.Warn(fmt.Sprintf("Failed to send initial message to logs client: %v", err))
		return
	}

	// Send history first
	for _, msg := range history {
		if _, err := conn.Write([]byte(msg)); err != nil {
			return
		}
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
	d.handleAttachWithHistory(conn, true, 20)
}

// handleAttachWithHistory streams raw slog output with configurable history
func (d *Daemon) handleAttachWithHistory(conn net.Conn, showHistory bool, historyLines int) {
	defer conn.Close()

	// Subscribe to slog broadcasts with history
	var logChan chan string
	var history []string
	if showHistory {
		logChan, history = d.logBroadcast.SubscribeWithHistory(historyLines)
	} else {
		logChan = d.logBroadcast.Subscribe()
	}
	defer d.logBroadcast.Unsubscribe(logChan)

	// Send initial message
	initialMsg := "Attached to overseer daemon. Press Ctrl+C to detach.\n\n"
	if _, err := conn.Write([]byte(initialMsg)); err != nil {
		return
	}

	// Send history first
	for _, msg := range history {
		if _, err := conn.Write([]byte(msg)); err != nil {
			return
		}
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
	d.handleLogsWithStateAndHistory(conn, true, 20)
}

// handleLogsWithStateAndHistory streams both slog and structured state logs with configurable history
func (d *Daemon) handleLogsWithStateAndHistory(conn net.Conn, showHistory bool, historyLines int) {
	// Subscribe to state log channel (which includes all events)
	var stateID uint64
	var stateChan <-chan state.LogEntry
	if showHistory {
		stateID, stateChan = stateOrchestrator.SubscribeLogsWithHistory(true, historyLines)
	} else {
		stateID, stateChan = stateOrchestrator.SubscribeLogsWithHistory(false, 0)
	}
	defer stateOrchestrator.UnsubscribeLogs(stateID)

	// Send initial message
	initialMsg := "Connected to overseer daemon logs. Press Ctrl+C to exit.\n"
	if _, err := conn.Write([]byte(initialMsg)); err != nil {
		slog.Warn(fmt.Sprintf("Failed to send initial message to logs client: %v", err))
		return
	}

	// Create renderer for formatting log entries
	renderer := state.NewLogRenderer(conn, false)

	// Render separator for history replay (only if showing history)
	if showHistory && historyLines > 0 {
		renderer.RenderSeparator("Recent History")
	}

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
