package cmd

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/daemon"
)

// OutputCache maintains a ring buffer of recent output lines for replay after daemon restart
type OutputCache struct {
	mu      sync.RWMutex
	lines   []string
	head    int
	count   int
	maxSize int
}

// NewOutputCache creates a new output cache with the specified size
func NewOutputCache(size int) *OutputCache {
	return &OutputCache{
		lines:   make([]string, size),
		maxSize: size,
	}
}

// Add adds a line to the cache
func (c *OutputCache) Add(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines[c.head] = line
	c.head = (c.head + 1) % c.maxSize
	if c.count < c.maxSize {
		c.count++
	}
}

// GetAll returns all cached lines in order (oldest first)
func (c *OutputCache) GetAll() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.count == 0 {
		return nil
	}
	result := make([]string, c.count)
	if c.count < c.maxSize {
		// Buffer not full yet, lines are at the start
		copy(result, c.lines[:c.count])
	} else {
		// Buffer is full, head points to oldest
		copy(result, c.lines[c.head:])
		copy(result[c.maxSize-c.head:], c.lines[:c.head])
	}
	return result
}

func NewCompanionRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "companion-run",
		Short:  "Internal companion wrapper (do not call directly)",
		Long:   `Internal command used by the daemon to run companion scripts. Do not call this directly.`,
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runCompanionWrapper()
		},
	}
}

// runCompanionWrapper is called when overseer is invoked as a companion wrapper.
// This runs as a separate process spawned by the daemon to wrap companion scripts.
// Environment variables:
//   - OVERSEER_COMPANION_RUN_ALIAS: tunnel alias
//   - OVERSEER_TUNNEL_TOKEN: authentication token
//   - OVERSEER_COMPANION_NAME: companion name
func runCompanionWrapper() {
	// Ignore SIGPIPE - crucial for surviving daemon death
	signal.Ignore(syscall.SIGPIPE)

	// Get required environment variables
	alias := os.Getenv("OVERSEER_COMPANION_RUN_ALIAS")
	token := os.Getenv("OVERSEER_TUNNEL_TOKEN")
	name := os.Getenv("OVERSEER_COMPANION_NAME")

	if alias == "" || token == "" || name == "" {
		fmt.Fprintf(os.Stderr, "companion-run: missing required environment variables\n")
		os.Exit(1)
	}

	// Validate token and get command from daemon (like askpass)
	response, err := daemon.SendCommand(fmt.Sprintf("COMPANION_INIT %s %s %s", alias, name, token))
	if err != nil {
		fmt.Fprintf(os.Stderr, "companion-run: failed to initialize: %v\n", err)
		os.Exit(1)
	}
	if len(response.Messages) == 0 || response.Messages[0].Status != "INFO" {
		errMsg := "unknown error"
		if len(response.Messages) > 0 {
			errMsg = response.Messages[0].Message
		}
		fmt.Fprintf(os.Stderr, "companion-run: %s\n", errMsg)
		os.Exit(1)
	}

	// Command is returned in the message (like askpass returns the password)
	command := response.Messages[0].Message

	// Derive socket path from alias + name
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("overseer-companion-%s-%s.sock", alias, name))

	// Run the actual wrapper logic
	executeCompanionWrapper(socketPath, alias, command)
}

// executeCompanionWrapper runs the companion script and streams output to the daemon socket
// Uses a PTY to enable terminal signal delivery (Ctrl+C) which can reach root-owned processes
func executeCompanionWrapper(socketPath, alias, command string) {
	// Expand ~ in command path
	expandedCmd := expandPath(command)

	// Spawn the actual script with tunnel alias as first argument
	cmd := exec.Command(expandedCmd, alias)
	cmd.Env = os.Environ()

	// Start with PTY - this gives us terminal signal delivery capability
	// When we write Ctrl+C (0x03) to the PTY, the terminal driver sends SIGINT
	// to the foreground process group, including root-owned processes like sudo
	ptmx, err := pty.Start(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "companion-run: failed to start with pty: %v\n", err)
		os.Exit(1)
	}
	defer ptmx.Close()

	// Channel to collect output lines (stdout/stderr merged in PTY)
	outputChan := make(chan string, 1000)

	// Read from PTY master
	var wg sync.WaitGroup
	wg.Add(1)
	go readPtyToChannel(ptmx, outputChan, &wg)

	// Close output channel when PTY reading is done
	go func() {
		wg.Wait()
		close(outputChan)
	}()

	// Create output cache for replay after daemon restart (1000 lines)
	outputCache := NewOutputCache(1000)

	// Output streaming goroutine - connects to daemon socket and streams output
	streamingDone := make(chan struct{})
	go func() {
		streamOutputToSocket(socketPath, outputChan, outputCache)
		close(streamingDone)
	}()

	// Signal handling - we'll send Ctrl+C to the PTY instead of syscall.Kill
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	childDone := make(chan error, 1)
	go func() {
		childDone <- cmd.Wait()
	}()

	// Wait for child to exit or signal to arrive
	var exitCode int
	select {
	case sig := <-sigChan:
		// Send Ctrl+C to PTY - terminal driver sends SIGINT to foreground process group
		// This works even for root-owned processes (like sudo openconnect)
		if sig == syscall.SIGINT || sig == syscall.SIGTERM {
			ptmx.Write([]byte{0x03}) // Ctrl+C
		} else if sig == syscall.SIGHUP {
			ptmx.Write([]byte{0x04}) // Ctrl+D (EOF) for SIGHUP
		}

		// Wait for child with timeout
		select {
		case err := <-childDone:
			// Child exited - get exit code
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = 1
				}
			}
		case <-time.After(5 * time.Second):
			// Force kill - close the PTY which should terminate the process
			ptmx.Close()
			// Also try direct kill as fallback
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			<-childDone // Wait for killed process
			exitCode = 137 // Killed
		}

	case err := <-childDone:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
	}

	// Wait for PTY output to drain so cleanup logs get captured
	wg.Wait()

	// Wait for all output to be sent to daemon (with timeout)
	select {
	case <-streamingDone:
		// All output sent successfully
	case <-time.After(5 * time.Second):
		// Timeout waiting for output - exit anyway
	}

	os.Exit(exitCode)
}

// expandPath expands ~ to home directory
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// readPtyToChannel reads lines from a PTY master and sends them to a channel
// PTY merges stdout and stderr, so we use a single [output] tag
func readPtyToChannel(ptmx *os.File, output chan<- string, wg *sync.WaitGroup) {
	defer wg.Done()

	reader := bufio.NewReader(ptmx)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return // PTY closed or error
		}

		// Format: timestamp [output] line (stdout/stderr merged in PTY)
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		formatted := fmt.Sprintf("%s [output] %s", timestamp, strings.TrimSuffix(line, "\n"))

		// Non-blocking send - drop if channel is full
		select {
		case output <- formatted:
		default:
		}
	}
}

// streamOutputToSocket connects to the daemon socket and streams output
// Handles reconnection when daemon restarts, replaying cached history
func streamOutputToSocket(socketPath string, output <-chan string, cache *OutputCache) {
	var conn net.Conn
	var writer *bufio.Writer
	var connMu sync.Mutex
	isReconnection := false
	connDied := make(chan struct{}, 1)

	// Helper to send a single line (caller must hold connMu)
	sendLine := func(line string) error {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			return err
		}
		return writer.Flush()
	}

	// Helper to send cached history on reconnection (caller must hold connMu)
	sendHistory := func() bool {
		lines := cache.GetAll()
		if err := sendLine("HISTORY_START"); err != nil {
			return false
		}
		for _, histLine := range lines {
			if err := sendLine(histLine); err != nil {
				return false
			}
		}
		if err := sendLine("HISTORY_END"); err != nil {
			return false
		}
		return true
	}

	// Helper to close connection (caller must hold connMu)
	closeConn := func() {
		if conn != nil {
			conn.Close()
			conn = nil
			writer = nil
		}
	}

	// Helper to establish connection (caller must hold connMu)
	connect := func() bool {
		for range 3 {
			var err error
			conn, err = net.DialTimeout("unix", socketPath, 1*time.Second)
			if err != nil {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			writer = bufio.NewWriter(conn)

			// Send cached history on reconnection
			if isReconnection {
				if !sendHistory() {
					closeConn()
					continue
				}
			}
			isReconnection = true

			// Start reader goroutine to detect connection death
			go func(c net.Conn) {
				buf := make([]byte, 1)
				c.Read(buf) // Blocks until connection dies or is closed
				select {
				case connDied <- struct{}{}:
				default:
				}
			}(conn)

			return true
		}
		return false
	}

	// Main loop: handle both output lines and connection death
	for {
		select {
		case line, ok := <-output:
			if !ok {
				// Output channel closed, clean up and exit
				connMu.Lock()
				closeConn()
				connMu.Unlock()
				return
			}

			connMu.Lock()
			// Connect if not connected
			if conn == nil {
				if !connect() {
					connMu.Unlock()
					continue // Drop line if can't connect
				}
			}

			// Try to write
			if err := sendLine(line); err != nil {
				closeConn()
				// Try to reconnect and resend
				if connect() {
					sendLine(line) // Best effort resend
				}
			}

			// Add to cache after attempting send
			cache.Add(line)
			connMu.Unlock()

		case <-connDied:
			// Connection died, reconnect proactively
			connMu.Lock()
			closeConn()
			connect() // Reconnect and send history
			connMu.Unlock()
		}
	}
}
