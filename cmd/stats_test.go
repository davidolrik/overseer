package cmd

import (
	"strings"
	"testing"
	"time"

	"go.olrik.dev/overseer/internal/db"
)

// rebootScenario models a firewall reboot: one long online session during
// which the public IP flips to a different address for ten seconds and back,
// followed by a five second blip and then a stable session.
func rebootScenario() (online, ip []db.SensorChange, start, end time.Time) {
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.Local)
	at := func(h, m, s int) time.Time {
		return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second)
	}

	ip = []db.SensorChange{
		{SensorName: "public_ipv4", NewValue: "90.184.132.136", Timestamp: day.Add(-12 * time.Hour)},
		{SensorName: "public_ipv4", NewValue: "212.27.7.95", Timestamp: at(13, 1, 20)},
		{SensorName: "public_ipv4", NewValue: "90.184.132.136", Timestamp: at(13, 1, 30)},
	}
	online = []db.SensorChange{
		{SensorName: "online", NewValue: "true", Timestamp: day.Add(-11 * time.Hour)},
		{SensorName: "online", NewValue: "false", Timestamp: at(8, 3, 31)},
		{SensorName: "online", NewValue: "true", Timestamp: at(8, 11, 41)},
		{SensorName: "online", NewValue: "false", Timestamp: at(13, 1, 51)},
		{SensorName: "online", NewValue: "true", Timestamp: at(13, 2, 21)},
		{SensorName: "online", NewValue: "false", Timestamp: at(13, 2, 26)},
		{SensorName: "online", NewValue: "true", Timestamp: at(13, 2, 29)},
	}
	return online, ip, day, at(13, 10, 0)
}

func TestParseOnlineSessionsKeepsSessionWholeAcrossIPChange(t *testing.T) {
	online, ip, start, end := rebootScenario()

	sessions := parseOnlineSessions(online, ip, start, end)

	if len(sessions) != 4 {
		t.Fatalf("expected 4 sessions, got %d: %+v", len(sessions), sessions)
	}
	long := sessions[1]
	if long.Duration != 4*time.Hour+50*time.Minute+10*time.Second {
		t.Errorf("session spanning the IP flip should stay whole, got duration %s", long.Duration)
	}
	if long.IP != "90.184.132.136" {
		t.Errorf("session should carry the IP it ended on, got %q", long.IP)
	}
}

func TestSplitSessionsByIPProducesOneSegmentPerIP(t *testing.T) {
	online, ip, start, end := rebootScenario()

	segments := splitSessionsByIP(parseOnlineSessions(online, ip, start, end), ip)

	if len(segments) != 6 {
		t.Fatalf("expected 6 segments, got %d: %+v", len(segments), segments)
	}
	if segments[2].IP != "212.27.7.95" || segments[2].Duration != 10*time.Second {
		t.Errorf("expected a 10s segment on 212.27.7.95, got %+v", segments[2])
	}
}

func TestNetworkQualityIgnoresIPSplitsWhenCountingBriefSessions(t *testing.T) {
	online, ip, start, end := rebootScenario()
	sessions := parseOnlineSessions(online, ip, start, end)

	out := captureStdout(t, func() { printNetworkQuality(sessions, start, end) })

	if strings.Contains(out, "brief sessions") {
		t.Errorf("IP flip inside a session must not count as brief sessions:\n%s", out)
	}
	if !strings.Contains(out, "Good") {
		t.Errorf("expected Good overall quality:\n%s", out)
	}
}

func TestIPQualityCountsBriefSessionsBeforeIPSplitting(t *testing.T) {
	online, ip, start, end := rebootScenario()
	sessions := parseOnlineSessions(online, ip, start, end)
	segments := splitSessionsByIP(sessions, ip)

	ipStats := groupSessionsByIP(segments, sessions, start, end, nil)

	var hq IPStats
	for _, s := range ipStats {
		if s.IP == "90.184.132.136" {
			hq = s
		}
	}
	if hq.SessionCount != 5 {
		t.Errorf("expected 5 IP segments for HQ, got %d", hq.SessionCount)
	}
	if hq.ShortSessions != 1 {
		t.Errorf("expected 1 brief session for HQ (the 5s blip), got %d", hq.ShortSessions)
	}

	quality, _, issues := assessIPQuality(hq)
	if quality != "Good" {
		t.Errorf("expected Good quality for HQ, got %s with issues %v", quality, issues)
	}
	for _, issue := range issues {
		if strings.Contains(issue, "consecutive brief") {
			t.Errorf("IP flip must not produce a brief-session streak: %v", issues)
		}
	}
}

func TestParseOnlineSessionsIgnoresIPClearedJustBeforeSleep(t *testing.T) {
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.Local)
	ip := []db.SensorChange{
		{SensorName: "public_ipv4", NewValue: "90.184.132.136", Timestamp: day.Add(-12 * time.Hour)},
		// At sleep the IP is cleared a moment before the online sensor goes false
		{SensorName: "public_ipv4", NewValue: "169.254.0.0", Timestamp: day.Add(8*time.Hour + 300*time.Millisecond)},
	}
	online := []db.SensorChange{
		{SensorName: "online", NewValue: "true", Timestamp: day.Add(-11 * time.Hour)},
		{SensorName: "online", NewValue: "false", Timestamp: day.Add(8*time.Hour + 600*time.Millisecond)},
	}
	start, end := day, day.Add(13*time.Hour)

	sessions := parseOnlineSessions(online, ip, start, end)
	segments := splitSessionsByIP(sessions, ip)

	if len(sessions) != 1 || sessions[0].IP != "90.184.132.136" {
		t.Fatalf("session should keep the IP it had while online, got %+v", sessions)
	}
	for _, s := range groupSessionsByIP(segments, sessions, start, end, nil) {
		if s.IP == "unknown" {
			t.Errorf("no unknown group expected, got %+v", s)
		}
	}
}

func TestParseOnlineSessionsDropsWakeUpArtifactWithoutIP(t *testing.T) {
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, time.Local)
	ip := []db.SensorChange{
		{SensorName: "public_ipv4", NewValue: "90.184.132.136", Timestamp: day.Add(-12 * time.Hour)},
		{SensorName: "public_ipv4", NewValue: "169.254.0.0", Timestamp: day.Add(-2 * time.Hour)},
	}
	online := []db.SensorChange{
		{SensorName: "online", NewValue: "true", Timestamp: day.Add(-11 * time.Hour)},
		{SensorName: "online", NewValue: "false", Timestamp: day.Add(-2 * time.Hour)},
		// Brief online flap on wake before the IP probe has run
		{SensorName: "online", NewValue: "true", Timestamp: day.Add(9 * time.Hour)},
		{SensorName: "online", NewValue: "false", Timestamp: day.Add(9*time.Hour + 5*time.Second)},
	}

	sessions := parseOnlineSessions(online, ip, day, day.Add(13*time.Hour))

	if len(sessions) != 0 {
		t.Errorf("wake-up artifact without a known IP should be dropped, got %+v", sessions)
	}
}

func TestSummaryCountsWholeSessionsNotIPSegments(t *testing.T) {
	online, ip, start, end := rebootScenario()

	out := captureStdout(t, func() { printStatsReport(online, ip, start, end, "test", nil) })

	if !strings.Contains(out, "Sessions:             \x1b[37m4\x1b[0m") {
		t.Errorf("expected summary to count 4 whole sessions:\n%s", out)
	}
	if !strings.Contains(out, "6 entries") {
		t.Errorf("expected the session list to still show 6 IP segments:\n%s", out)
	}
}
