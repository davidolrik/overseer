package state

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// LogStreamer manages streaming logs to multiple clients.
// It maintains a ring buffer of recent entries for replay and
// broadcasts new entries to all connected clients.
type LogStreamer struct {
	mu          sync.RWMutex
	clients     map[uint64]chan LogEntry
	nextID      uint64
	ringBuffer  *RingBuffer[LogEntry]
	bufferSize  int // Per-client channel buffer size
}

// NewLogStreamer creates a new log streamer
// historySize determines how many recent entries to keep for replay
func NewLogStreamer(historySize int) *LogStreamer {
	return &LogStreamer{
		clients:    make(map[uint64]chan LogEntry),
		ringBuffer: NewRingBuffer[LogEntry](historySize),
		bufferSize: 64,
	}
}

// Emit broadcasts a log entry to all connected clients
func (ls *LogStreamer) Emit(entry LogEntry) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	// Add to ring buffer for replay
	ls.ringBuffer.Push(entry)

	// Broadcast to all clients
	for id, ch := range ls.clients {
		select {
		case ch <- entry:
		default:
			// Client not keeping up - they'll miss this entry
			// Could optionally close slow clients here
			_ = id // Silence unused variable warning
		}
	}
}

// Subscribe adds a new client to receive log entries.
// If replay is true, recent history is sent first.
// Returns the client ID (for unsubscribing) and the receive channel.
func (ls *LogStreamer) Subscribe(replay bool) (uint64, <-chan LogEntry) {
	// Use full history when replay is true
	lines := ls.ringBuffer.Len()
	if !replay {
		lines = 0
	}
	return ls.SubscribeWithHistory(replay, lines)
}

// SubscribeWithHistory adds a new client to receive log entries.
// If replay is true, the last 'lines' entries from history are sent first.
// Returns the client ID (for unsubscribing) and the receive channel.
func (ls *LogStreamer) SubscribeWithHistory(replay bool, lines int) (uint64, <-chan LogEntry) {
	ch := make(chan LogEntry, ls.bufferSize)

	ls.mu.Lock()
	id := ls.nextID
	ls.nextID++
	ls.clients[id] = ch

	// Send replay before unlocking to ensure ordering
	if replay && lines > 0 {
		items := ls.ringBuffer.Items()
		// Limit to last N items
		start := len(items) - lines
		if start < 0 {
			start = 0
		}
		for _, entry := range items[start:] {
			select {
			case ch <- entry:
			default:
				// Buffer full during replay - skip older entries
			}
		}
	}
	ls.mu.Unlock()

	return id, ch
}

// Unsubscribe removes a client from receiving broadcasts
func (ls *LogStreamer) Unsubscribe(id uint64) {
	ls.mu.Lock()
	if ch, ok := ls.clients[id]; ok {
		close(ch)
		delete(ls.clients, id)
	}
	ls.mu.Unlock()
}

// ClientCount returns the number of connected clients
func (ls *LogStreamer) ClientCount() int {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return len(ls.clients)
}

// RingBuffer is a fixed-size circular buffer
type RingBuffer[T any] struct {
	items []T
	size  int
	head  int // Next write position
	count int // Number of items currently in buffer
}

// NewRingBuffer creates a new ring buffer with the given capacity
func NewRingBuffer[T any](size int) *RingBuffer[T] {
	return &RingBuffer[T]{
		items: make([]T, size),
		size:  size,
	}
}

// Push adds an item to the buffer, overwriting the oldest if full
func (rb *RingBuffer[T]) Push(item T) {
	rb.items[rb.head] = item
	rb.head = (rb.head + 1) % rb.size
	if rb.count < rb.size {
		rb.count++
	}
}

// Items returns all items in the buffer, oldest first
func (rb *RingBuffer[T]) Items() []T {
	if rb.count == 0 {
		return nil
	}

	result := make([]T, rb.count)
	if rb.count < rb.size {
		// Buffer not full - items are 0 to count-1
		copy(result, rb.items[:rb.count])
	} else {
		// Buffer full - oldest is at head, wrap around
		copy(result, rb.items[rb.head:])
		copy(result[rb.size-rb.head:], rb.items[:rb.head])
	}

	return result
}

// Len returns the number of items in the buffer
func (rb *RingBuffer[T]) Len() int {
	return rb.count
}

// Clear removes all items from the buffer
func (rb *RingBuffer[T]) Clear() {
	rb.head = 0
	rb.count = 0
}

// LogRenderer formats log entries for display
type LogRenderer struct {
	out     io.Writer
	noColor bool
}

// NewLogRenderer creates a new log renderer
func NewLogRenderer(out io.Writer, noColor bool) *LogRenderer {
	return &LogRenderer{out: out, noColor: noColor}
}

// Render formats and writes a log entry
func (r *LogRenderer) Render(entry LogEntry) {
	// Timestamp
	ts := entry.Timestamp.Format("15:04:05.000")

	// Level
	level := r.levelString(entry.Level)

	// Category icon
	icon := entry.Category.Icon()

	// Format based on entry type
	switch {
	case entry.Sensor != nil:
		r.renderSensor(ts, level, icon, entry.Sensor)
	case entry.Transition != nil:
		r.renderTransition(ts, level, icon, entry.Transition)
	case entry.Effect != nil:
		r.renderEffect(ts, level, icon, entry.Effect)
	case entry.System != nil:
		r.renderSystem(ts, level, icon, entry.Message, entry.System)
	case entry.Hook != nil:
		r.renderHook(ts, level, icon, entry.Hook)
	default:
		fmt.Fprintf(r.out, "%s %s %s %s\n", ts, level, icon, entry.Message)
	}
}

func (r *LogRenderer) levelString(level LogLevel) string {
	if r.noColor {
		return level.String()
	}

	switch level {
	case LogDebug:
		return "\033[90mDBG\033[0m"  // Gray
	case LogInfo:
		return "\033[32mINF\033[0m"  // Green
	case LogWarn:
		return "\033[33mWRN\033[0m"  // Yellow
	case LogError:
		return "\033[31mERR\033[0m" // Red
	default:
		return level.String()
	}
}

func (r *LogRenderer) renderSensor(ts, level, icon string, s *SensorLogData) {
	fmt.Fprintf(r.out, "%s %s %s %s: ", ts, level, icon, s.Name)

	if s.Online != nil {
		fmt.Fprintf(r.out, "online=%v", *s.Online)
	} else if s.IP != "" {
		fmt.Fprintf(r.out, "%s", s.IP)
	} else if s.Value != "" {
		fmt.Fprintf(r.out, "%s", s.Value)
	}

	if s.Latency > 0 {
		fmt.Fprintf(r.out, " (%s)", s.Latency.Round(time.Millisecond))
	}

	if s.Error != "" {
		fmt.Fprintf(r.out, " error: %s", s.Error)
	}

	fmt.Fprintln(r.out)
}

func (r *LogRenderer) renderTransition(ts, level, icon string, t *TransitionLogData) {
	arrow := "->"
	if !r.noColor {
		arrow = "\033[90m->\033[0m"
	}

	field := t.Field
	if !r.noColor {
		// Color the field name based on type
		switch t.Field {
		case "online":
			field = "\033[36monline\033[0m" // Cyan
		case "context":
			field = "\033[35mcontext\033[0m" // Magenta
		case "location":
			field = "\033[34mlocation\033[0m" // Blue
		case "ipv4", "ipv6":
			field = "\033[33m" + t.Field + "\033[0m" // Yellow
		case "system_power":
			field = "\033[1m\033[93msystem_power\033[0m" // Bold bright yellow
		}
	}

	// Color values for awake/sleeping and true/false
	from := t.From
	to := t.To
	if !r.noColor {
		switch from {
		case "true", "awake":
			from = "\033[32m" + from + "\033[0m" // Green
		case "false", "sleeping":
			from = "\033[31m" + from + "\033[0m" // Red
		}
		switch to {
		case "true", "awake":
			to = "\033[32m" + to + "\033[0m" // Green
		case "false", "sleeping":
			to = "\033[31m" + to + "\033[0m" // Red
		}
	}

	fmt.Fprintf(r.out, "%s %s %s %s: %s %s %s",
		ts, level, icon, field, from, arrow, to)

	if t.Source != "" {
		if r.noColor {
			fmt.Fprintf(r.out, " (%s)", t.Source)
		} else {
			fmt.Fprintf(r.out, " \033[90m(%s)\033[0m", t.Source)
		}
	}

	fmt.Fprintln(r.out)
}

func (r *LogRenderer) renderEffect(ts, level, icon string, e *EffectLogData) {
	status := "ok"
	if !e.Success {
		status = "failed"
		if !r.noColor {
			status = "\033[31mfailed\033[0m"
		}
	} else if !r.noColor {
		status = "\033[32mok\033[0m"
	}

	fmt.Fprintf(r.out, "%s %s %s %s: %s [%s]",
		ts, level, icon, e.Name, e.Target, status)

	if e.Duration > 0 {
		fmt.Fprintf(r.out, " %s", e.Duration.Round(time.Millisecond))
	}

	if e.Error != "" {
		fmt.Fprintf(r.out, " - %s", e.Error)
	}

	fmt.Fprintln(r.out)
}

func (r *LogRenderer) renderSystem(ts, level, icon, message string, s *SystemLogData) {
	fmt.Fprintf(r.out, "%s %s %s %s", ts, level, icon, s.Event)

	if s.Details != "" {
		fmt.Fprintf(r.out, ": %s", s.Details)
	} else if message != "" {
		fmt.Fprintf(r.out, ": %s", message)
	}

	fmt.Fprintln(r.out)
}

func (r *LogRenderer) renderHook(ts, level, icon string, h *HookLogData) {
	// Format: "15:04:05.000 INF ! enter location: home [ok] 150ms"
	hookType := h.Type
	targetType := h.TargetType
	if !r.noColor {
		// Color the hook type
		switch h.Type {
		case "enter":
			hookType = "\033[32menter\033[0m" // Green
		case "leave":
			hookType = "\033[33mleave\033[0m" // Yellow
		}
		// Color the target type
		switch h.TargetType {
		case "location":
			targetType = "\033[34mlocation\033[0m" // Blue
		case "context":
			targetType = "\033[35mcontext\033[0m" // Magenta
		}
	}

	fmt.Fprintf(r.out, "%s %s %s %s %s: %s",
		ts, level, icon, hookType, targetType, h.Target)

	// Status
	status := "ok"
	if !h.Success {
		status = "failed"
		if !r.noColor {
			status = "\033[31mfailed\033[0m"
		}
	} else if !r.noColor {
		status = "\033[32mok\033[0m"
	}
	fmt.Fprintf(r.out, " [%s]", status)

	// Duration
	if h.Duration > 0 {
		fmt.Fprintf(r.out, " %s", h.Duration.Round(time.Millisecond))
	}

	// Error
	if h.Error != "" {
		fmt.Fprintf(r.out, " - %s", h.Error)
	}

	fmt.Fprintln(r.out)

	// Show command (dimmed)
	if h.Command != "" {
		if r.noColor {
			fmt.Fprintf(r.out, "    cmd: %s\n", h.Command)
		} else {
			fmt.Fprintf(r.out, "    \033[90mcmd: %s\033[0m\n", h.Command)
		}
	}

	// Show output if present (truncated, dimmed)
	if h.Output != "" {
		lines := strings.Split(h.Output, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			if r.noColor {
				fmt.Fprintf(r.out, "    | %s\n", line)
			} else {
				fmt.Fprintf(r.out, "    \033[90m| %s\033[0m\n", line)
			}
		}
	}
}

// RenderSeparator renders a visual separator
func (r *LogRenderer) RenderSeparator(title string) {
	if r.noColor {
		fmt.Fprintf(r.out, "--- %s ---\n", title)
	} else {
		fmt.Fprintf(r.out, "\033[90m--- %s ---\033[0m\n", title)
	}
}
