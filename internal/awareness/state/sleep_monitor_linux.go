package state

import (
	"context"
	"os"

	"github.com/godbus/dbus/v5"
)

// Start begins listening for system sleep/wake events via D-Bus (logind).
// Falls back to no-op if D-Bus is unavailable (e.g., headless servers).
func (m *SleepMonitor) Start(ctx context.Context) {
	go func() {
		conn, err := dbus.SystemBus()
		if err != nil {
			// D-Bus unavailable — common on headless servers that don't sleep
			if os.Getenv("DBUS_SYSTEM_BUS_ADDRESS") == "" {
				m.logger.Debug("D-Bus unavailable, sleep monitor disabled (headless server?)")
			} else {
				m.logger.Warn("Failed to connect to D-Bus for sleep monitoring", "error", err)
			}
			return
		}

		if err := conn.AddMatchSignal(
			dbus.WithMatchObjectPath("/org/freedesktop/login1"),
			dbus.WithMatchInterface("org.freedesktop.login1.Manager"),
			dbus.WithMatchMember("PrepareForSleep"),
		); err != nil {
			m.logger.Warn("Failed to subscribe to PrepareForSleep signal", "error", err)
			return
		}

		signals := make(chan *dbus.Signal, 8)
		conn.Signal(signals)

		m.logger.Info("Sleep monitor started (D-Bus logind)")

		for {
			select {
			case <-ctx.Done():
				conn.RemoveSignal(signals)
				m.logger.Debug("Sleep monitor stopped")
				return
			case sig := <-signals:
				if sig == nil {
					return
				}
				if sig.Name != "org.freedesktop.login1.Manager.PrepareForSleep" {
					continue
				}
				if len(sig.Body) < 1 {
					continue
				}
				entering, ok := sig.Body[0].(bool)
				if !ok {
					continue
				}
				if entering {
					m.markSleep()
				} else {
					m.markWake()
				}
			}
		}
	}()
}
