//go:build darwin

package security

import (
	"context"
	"log/slog"
	"net"
	"os/exec"
	"time"
)

// NetworkMonitor watches for network changes on Darwin/macOS
type NetworkMonitor struct {
	events   chan struct{}
	logger   *slog.Logger
	interval time.Duration
}

// NewNetworkMonitor creates a new network change monitor for macOS
func NewNetworkMonitor(logger *slog.Logger) *NetworkMonitor {
	return &NetworkMonitor{
		events:   make(chan struct{}, 1),
		logger:   logger,
		interval: 5 * time.Second, // Poll every 5 seconds for faster response
	}
}

// Start begins monitoring for network changes
func (nm *NetworkMonitor) Start(ctx context.Context) {
	nm.logger.Info("Network monitor started (macOS polling)", "interval", nm.interval)

	// Poll network interfaces periodically
	// macOS doesn't have a reliable event-based network monitoring command
	// that works without elevated privileges, so we use polling
	go nm.pollNetworkChanges(ctx)
}

// pollNetworkChanges periodically checks for network changes as a fallback
func (nm *NetworkMonitor) pollNetworkChanges(ctx context.Context) {
	ticker := time.NewTicker(nm.interval)
	defer ticker.Stop()

	var lastState string

	// Trigger initial check immediately
	nm.logger.Debug("Performing initial network state check")
	select {
	case nm.events <- struct{}{}:
	default:
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get current network state
			currentState := nm.getNetworkState()

			// Check if state changed
			if lastState != "" && currentState != lastState {
				nm.logger.Debug("Network change detected (poll)",
					"old", lastState,
					"new", currentState)

				// Trigger event (non-blocking)
				select {
				case nm.events <- struct{}{}:
				default:
					// Event already pending, skip
				}
			}

			lastState = currentState
		}
	}
}

// getNetworkState returns a string representing the current network state
func (nm *NetworkMonitor) getNetworkState() string {
	// Get default route to detect which interface is active
	cmd := exec.Command("route", "-n", "get", "default")
	output, err := cmd.Output()
	if err != nil {
		return "no_route"
	}

	route := string(output)

	// Also check if we can resolve DNS to detect actual connectivity
	// This catches the case where route exists but network is not working
	// Use a quick DNS check (timeout 2 seconds)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Try both OpenDNS resolvers for robustness
	resolvers := []string{"resolver1.opendns.com", "resolver2.opendns.com"}
	connectivity := "offline"
	for _, resolver := range resolvers {
		_, err = (&net.Resolver{}).LookupHost(ctx, resolver)
		if err == nil {
			connectivity = "online"
			break
		}
	}

	// Combine route info and connectivity status
	return route + "\n---connectivity:" + connectivity
}

// Events returns a channel that receives notifications when network changes
func (nm *NetworkMonitor) Events() <-chan struct{} {
	return nm.events
}

// TriggerCheck manually triggers a network check event
func (nm *NetworkMonitor) TriggerCheck() {
	select {
	case nm.events <- struct{}{}:
	default:
		// Event already pending
	}
}
