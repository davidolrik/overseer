//go:build !darwin

package state

// isUserActive always returns true on non-darwin platforms.
// Dark wake (Power Nap) is a macOS-specific feature.
func (m *SleepMonitor) isUserActive() bool {
	return true
}
