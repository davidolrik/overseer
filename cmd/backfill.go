package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/db"
)

func NewBackfillCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "backfill-sleep",
		Short:  "Backfill online=false events at historical sleep boundaries",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runBackfillSleep()
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:    "undo",
		Short:  "Remove all backfilled online events injected by previous runs",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			undoBackfillSleep()
		},
	})

	return cmd
}

func runBackfillSleep() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get home directory: %v\n", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(homeDir, ".config", "overseer", "overseer.db")
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	const wakeWindow = 5 * time.Minute
	totalInjected := 0

	// Run passes in a loop until convergence, since later passes create
	// state that earlier passes need to process (e.g. IP restores at wake
	// create new IPs that need clearing at the next sleep).
	for round := 1; ; round++ {
		injected := 0

		// Pass 1: Repair missing online=true after wake events.
		events := loadEvents(database)
		for _, e := range events {
			if e.SensorName != "system_power" || e.NewValue != "awake" {
				continue
			}
			hasOnline, err := database.HasSensorChangeAfter("online", "true", e.Timestamp, wakeWindow)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error checking wake at %s: %v\n", e.Timestamp.Format(time.DateTime), err)
				continue
			}
			if !hasOnline {
				inserted, err := injectEvent(database, "false", "true", e.Timestamp)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error injecting online at %s: %v\n", e.Timestamp.Format(time.DateTime), err)
					continue
				}
				if inserted {
					injected++
					fmt.Printf("  repaired online=true  at %s (wake)\n", e.Timestamp.Format(time.DateTime))
				}
			}
		}

		// Pass 2: Inject online=false and clear public_ipv4 at sleep boundaries.
		events = loadEvents(database)
		currentOnline := false
		currentIP := ""
		for _, e := range events {
			switch {
			case e.SensorName == "online":
				currentOnline = e.NewValue == "true"

			case e.SensorName == "public_ipv4":
				currentIP = e.NewValue

			case e.SensorName == "system_power" && e.NewValue == "sleeping":
				if currentOnline {
					inserted, err := injectEvent(database, "true", "false", e.Timestamp)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Error injecting offline at %s: %v\n", e.Timestamp.Format(time.DateTime), err)
						continue
					}
					if inserted {
						injected++
						fmt.Printf("  injected online=false at %s (sleep)\n", e.Timestamp.Format(time.DateTime))
					}
					currentOnline = false
				}
				if currentIP != "" && currentIP != "169.254.0.0" {
					inserted, err := injectIPChange(database, currentIP, "169.254.0.0", e.Timestamp)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Error clearing IP at %s: %v\n", e.Timestamp.Format(time.DateTime), err)
						continue
					}
					if inserted {
						injected++
						fmt.Printf("  cleared  public_ipv4  at %s (was %s)\n", e.Timestamp.Format(time.DateTime), currentIP)
					}
					currentIP = "169.254.0.0"
				}
			}
		}

		// Pass 3: Restore public IP at wake if the network didn't change.
		events = loadEvents(database)
		var preSleepIP string
		for i, e := range events {
			switch {
			case e.SensorName == "public_ipv4":
				if e.NewValue != "169.254.0.0" {
					preSleepIP = e.NewValue
				}

			case e.SensorName == "system_power" && e.NewValue == "awake":
				if preSleepIP == "" {
					continue
				}
				ipProbeRan := false
				for _, f := range events[i+1:] {
					if f.Timestamp.After(e.Timestamp.Add(wakeWindow)) {
						break
					}
					if f.SensorName == "public_ipv4" && f.NewValue != "169.254.0.0" {
						ipProbeRan = true
						break
					}
				}
				if ipProbeRan {
					continue
				}
				inserted, err := injectIPChange(database, "169.254.0.0", preSleepIP, e.Timestamp)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error restoring IP at %s: %v\n", e.Timestamp.Format(time.DateTime), err)
					continue
				}
				if inserted {
					injected++
					fmt.Printf("  restored public_ipv4  at %s (to %s)\n", e.Timestamp.Format(time.DateTime), preSleepIP)
				}
			}
		}

		totalInjected += injected
		if injected == 0 {
			break
		}
		fmt.Printf("  -- round %d: %d events, running again --\n", round, injected)
	}

	fmt.Printf("\nDone. Injected %d events.\n", totalInjected)
}

// loadEvents fetches all online and system_power sensor changes in chronological order.
func loadEvents(database *db.DB) []db.SensorChange {
	allChanges, err := database.GetRecentSensorChanges(1000000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to query sensor changes: %v\n", err)
		os.Exit(1)
	}

	// Reverse to chronological order (GetRecentSensorChanges returns DESC)
	for i, j := 0, len(allChanges)-1; i < j; i, j = i+1, j-1 {
		allChanges[i], allChanges[j] = allChanges[j], allChanges[i]
	}

	var events []db.SensorChange
	for _, c := range allChanges {
		if c.SensorName == "online" || c.SensorName == "system_power" || c.SensorName == "public_ipv4" {
			events = append(events, c)
		}
	}
	return events
}

func undoBackfillSleep() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get home directory: %v\n", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(homeDir, ".config", "overseer", "overseer.db")
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to open database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Get all system_power events to find where we injected
	allChanges, err := database.GetRecentSensorChanges(1000000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to query sensor changes: %v\n", err)
		os.Exit(1)
	}

	deleted := 0
	for _, c := range allChanges {
		if c.SensorName != "system_power" {
			continue
		}

		// Remove online events injected at sleep boundaries
		if c.NewValue == "sleeping" {
			n, err := database.DeleteSensorChangesNear("online", "true", "false", c.Timestamp, time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error deleting at %s: %v\n", c.Timestamp.Format(time.DateTime), err)
				continue
			}
			if n > 0 {
				deleted += int(n)
				fmt.Printf("  removed %d online=false at %s (sleep)\n", n, c.Timestamp.Format(time.DateTime))
			}
		}

		// Remove online events injected at wake boundaries (from old runs)
		if c.NewValue == "awake" {
			n, err := database.DeleteSensorChangesNear("online", "false", "true", c.Timestamp, time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error deleting at %s: %v\n", c.Timestamp.Format(time.DateTime), err)
				continue
			}
			if n > 0 {
				deleted += int(n)
				fmt.Printf("  removed %d online=true  at %s (wake)\n", n, c.Timestamp.Format(time.DateTime))
			}
		}
	}

	fmt.Printf("\nDone. Removed %d events.\n", deleted)
}

// injectEvent inserts an online sensor change if one doesn't already exist within 1 second.
// Returns true if a new row was inserted.
func injectEvent(database *db.DB, oldValue, newValue string, timestamp time.Time) (bool, error) {
	exists, err := database.HasSensorChangeNear("online", timestamp, time.Second)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	return true, database.LogSensorChangeAt("online", "bool", oldValue, newValue, timestamp)
}

// injectIPChange inserts a public_ipv4 change if one doesn't already exist within 1 second.
func injectIPChange(database *db.DB, oldIP, newIP string, timestamp time.Time) (bool, error) {
	exists, err := database.HasSensorChangeNear("public_ipv4", timestamp, time.Second)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	return true, database.LogSensorChangeAt("public_ipv4", "string", oldIP, newIP, timestamp)
}
