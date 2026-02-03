package state

import (
	"log/slog"
	"sync"
	"time"
)

// SleepMonitor detects system sleep/wake events and suppresses probes
// during sleep and for a grace period after wake to prevent flapping.
type SleepMonitor struct {
	mu        sync.RWMutex
	sleeping  bool
	wakeTime  time.Time
	graceTime time.Duration
	logger    *slog.Logger
	onSleep   func()
	onWake    func()
}

// NewSleepMonitor creates a new SleepMonitor with the given callbacks.
// onSleep and onWake are called when the system transitions to sleep or wake.
func NewSleepMonitor(logger *slog.Logger, onSleep, onWake func()) *SleepMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &SleepMonitor{
		graceTime: 10 * time.Second,
		logger:    logger,
		onSleep:   onSleep,
		onWake:    onWake,
	}
}

// IsSuppressed returns true if probes should be suppressed.
// This is the case when the system is sleeping, in dark wake (Power Nap),
// or within the grace period after wake.
func (m *SleepMonitor) IsSuppressed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.sleeping {
		return true
	}

	// Check for dark wake only in the first 2 seconds after wake
	// After that, we assume it's a full wake regardless of IOPMUserIsActive
	if !m.wakeTime.IsZero() {
		timeSinceWake := time.Since(m.wakeTime)

		// During first 2 seconds: if user not active, it might be dark wake
		if timeSinceWake < 2*time.Second && !m.isUserActive() {
			return true
		}

		// During grace period (10 seconds): always suppress
		if timeSinceWake < m.graceTime {
			return true
		}
	}

	return false
}

func (m *SleepMonitor) markSleep() {
	m.mu.Lock()
	m.sleeping = true
	m.mu.Unlock()

	m.logger.Info("System entering sleep")

	if m.onSleep != nil {
		m.onSleep()
	}
}

func (m *SleepMonitor) markWake() {
	m.mu.Lock()
	wasSleeping := m.sleeping
	if !wasSleeping {
		m.mu.Unlock()
		return // Already awake
	}
	m.sleeping = false
	m.wakeTime = time.Now()
	m.mu.Unlock()

	m.logger.Info("System waking up")

	if m.onWake != nil {
		m.onWake()
	}
}

// IsSleeping returns true if the system is currently marked as sleeping.
func (m *SleepMonitor) IsSleeping() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sleeping
}
