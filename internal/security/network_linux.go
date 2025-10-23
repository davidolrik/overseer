//go:build linux

package security

import (
	"context"
	"log/slog"
	"net"
	"os/exec"
	"time"
)

// NetworkMonitor watches for network changes on Linux
type NetworkMonitor struct {
	events   chan struct{}
	logger   *slog.Logger
	interval time.Duration
}

// NewNetworkMonitor creates a new network change monitor for Linux
func NewNetworkMonitor(logger *slog.Logger) *NetworkMonitor {
	return &NetworkMonitor{
		events:   make(chan struct{}, 1),
		logger:   logger,
		interval: 5 * time.Second, // Poll every 5 seconds for faster response
	}
}

// Start begins monitoring for network changes
func (nm *NetworkMonitor) Start(ctx context.Context) {
	// Use ip monitor to watch for network changes on Linux
	cmd := exec.CommandContext(ctx, "ip", "monitor", "link", "address", "route")

	// Start the command
	if err := cmd.Start(); err != nil {
		nm.logger.Error("Failed to start network monitor", "error", err)
		return
	}

	nm.logger.Info("Network monitor started (Linux ip monitor)")

	// Alternative approach: poll network interfaces periodically as fallback
	go nm.pollNetworkChanges(ctx)

	// Wait for command to finish
	go func() {
		if err := cmd.Wait(); err != nil {
			select {
			case <-ctx.Done():
				nm.logger.Debug("Network monitor stopped")
			default:
				nm.logger.Warn("Network monitor exited", "error", err)
			}
		}
	}()
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
			currentState := nm.getNetworkState()

			if lastState != "" && currentState != lastState {
				nm.logger.Debug("Network change detected (poll)")

				select {
				case nm.events <- struct{}{}:
				default:
				}
			}

			lastState = currentState
		}
	}
}

// getNetworkState returns a string representing the current network state
func (nm *NetworkMonitor) getNetworkState() string {
	cmd := exec.Command("ip", "route", "show", "default")
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
	}
}
