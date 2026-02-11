package cmd

import (
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"overseer.olrik.dev/internal/core"
	"overseer.olrik.dev/internal/db"
	"overseer.olrik.dev/internal/awareness"
)

// ANSI color codes
const (
	colorReset     = "\033[0m"
	colorBold      = "\033[1m"
	colorDim       = "\033[2m"
	colorRed       = "\033[31m"
	colorGreen     = "\033[32m"
	colorYellow    = "\033[33m"
	colorBlue      = "\033[34m"
	colorMagenta   = "\033[35m"
	colorCyan      = "\033[36m"
	colorWhite     = "\033[37m"
	colorGray      = "\033[90m"
	colorBoldGreen = "\033[1;32m"
	colorBoldRed   = "\033[1;31m"
)

// Predefined vibrant 24-bit colors for IPs (easily distinguishable)
// Avoids cyan, red, green, yellow which are used in UI indicators
var ipColors = []struct{ r, g, b uint8 }{
	{255, 107, 107}, // Coral
	{199, 125, 255}, // Purple
	{255, 190, 118}, // Peach/orange
	{116, 185, 255}, // Sky blue
	{255, 234, 167}, // Cream yellow
	{162, 155, 254}, // Lavender
	{255, 159, 243}, // Pink
	{255, 165, 2},   // Orange
	{255, 127, 80},  // Coral orange
	{218, 112, 214}, // Orchid
	{255, 182, 193}, // Light pink
	{244, 164, 96},  // Sandy brown
}

// ipColorCache stores assigned colors for IPs
var (
	ipColorCache = make(map[string]string)
	ipColorIndex = 0
)

// getIPColor returns a consistent 24-bit color for an IP address
func getIPColor(ip string) string {
	if ip == "" || ip == "unknown" {
		return colorGray
	}

	// Check cache first
	if color, exists := ipColorCache[ip]; exists {
		return color
	}

	// Assign next predefined color, or generate one if we run out
	var r, g, b uint8
	if ipColorIndex < len(ipColors) {
		c := ipColors[ipColorIndex]
		r, g, b = c.r, c.g, c.b
		ipColorIndex++
	} else {
		// Generate color from hash for additional IPs
		h := fnv.New32a()
		h.Write([]byte(ip))
		hash := h.Sum32()

		// Use golden ratio to spread hue values
		hue := float64(hash%360) / 360.0
		r, g, b = hslToRGB(hue, 0.7, 0.6)
	}

	// Create 24-bit color escape sequence
	color := fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
	ipColorCache[ip] = color
	return color
}

// hslToRGB converts HSL to RGB (h: 0-1, s: 0-1, l: 0-1)
func hslToRGB(h, s, l float64) (r, g, b uint8) {
	var fR, fG, fB float64

	if s == 0 {
		fR, fG, fB = l, l, l
	} else {
		var q float64
		if l < 0.5 {
			q = l * (1 + s)
		} else {
			q = l + s - l*s
		}
		p := 2*l - q
		fR = hueToRGB(p, q, h+1.0/3.0)
		fG = hueToRGB(p, q, h)
		fB = hueToRGB(p, q, h-1.0/3.0)
	}

	return uint8(fR * 255), uint8(fG * 255), uint8(fB * 255)
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	if t < 1.0/6.0 {
		return p + (q-p)*6*t
	}
	if t < 1.0/2.0 {
		return q
	}
	if t < 2.0/3.0 {
		return p + (q-p)*(2.0/3.0-t)*6
	}
	return p
}

// OnlineSession represents a period of being online
type OnlineSession struct {
	Start    time.Time
	End      time.Time
	Duration time.Duration
	IP       string // Public IP during this session
}

// IPStats holds statistics for a specific IP/network
type IPStats struct {
	IP            string
	LocationName  string // Location name from config (if matched)
	Sessions      []OnlineSession
	TotalOnline   time.Duration
	SessionCount  int
	ShortSessions int // Sessions < 5 minutes
}

// getLocationForIP finds the location name that matches a given IP address
// by checking the public_ip conditions in each location's configuration
func getLocationForIP(ip string, config *core.Configuration) string {
	if config == nil || ip == "" || ip == "unknown" {
		return ""
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return ""
	}

	for _, location := range config.Locations {
		if location == nil {
			continue
		}

		// Collect IP patterns from both simple Conditions map and structured Condition
		var ipPatterns []string

		// Check simple conditions map
		if location.Conditions != nil {
			if patterns, exists := location.Conditions["public_ip"]; exists {
				ipPatterns = append(ipPatterns, patterns...)
			}
		}

		// Check structured condition (if it's a awareness.Condition)
		if location.Condition != nil {
			if cond, ok := location.Condition.(awareness.Condition); ok {
				patterns := awareness.ExtractPatternsForSensor(cond, "public_ipv4")
				ipPatterns = append(ipPatterns, patterns...)
			}
		}

		// Check if any pattern matches the IP
		for _, pattern := range ipPatterns {
			// Check if it's a CIDR range
			if _, cidr, err := net.ParseCIDR(pattern); err == nil {
				if cidr.Contains(parsedIP) {
					if location.DisplayName != "" {
						return location.DisplayName
					}
					return location.Name
				}
			} else if pattern == ip {
				// Exact IP match
				if location.DisplayName != "" {
					return location.DisplayName
				}
				return location.Name
			}
		}
	}

	return ""
}

func NewStatsCommand() *cobra.Command {
	var sinceStr string
	var days int

	statsCmd := &cobra.Command{
		Use:     "qa",
		Aliases: []string{"q", "statistics", "stats", "stat"},
		Short:   "Show connectivity statistics and session history",
		Long: `Display statistics about online sessions and network quality.

Shows all online sessions with their duration, helping identify network
stability issues through patterns of frequent connects/disconnects.

Examples:
  overseer stats                     # Today only
  overseer stats -s yesterday        # Just yesterday
  overseer stats -s yesterday -d 2   # Yesterday and today
  overseer stats -d 7                # Last 7 days
  overseer stats -s 2025-12-01       # Just Dec 1st
  overseer stats -s 2025-12-01 -d 3  # Dec 1-3`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			// If -d is specified but -s is not, go backwards from today
			sinceChanged := cmd.Flags().Changed("since")
			start, end, label := parseDateRange(sinceStr, days, sinceChanged)
			runStats(start, end, label)
		},
	}

	statsCmd.Flags().StringVarP(&sinceStr, "since", "S", "today", "Start date: today, yesterday, or YYYY-MM-DD")
	statsCmd.Flags().IntVarP(&days, "days", "D", 1, "Number of days to include")

	return statsCmd
}

// parseDateRange converts since flag and days into a date range
func parseDateRange(sinceStr string, days int, sinceSpecified bool) (start, end time.Time, label string) {
	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// If days > 1 but since wasn't specified, go backwards from today
	if days > 1 && !sinceSpecified {
		start = startOfToday.AddDate(0, 0, -(days - 1))
		end = now
		label = fmt.Sprintf("last %d days", days)
		return start, end, label
	}

	// Parse the start date
	switch sinceStr {
	case "today", "":
		start = startOfToday
	case "yesterday":
		start = startOfToday.AddDate(0, 0, -1)
	default:
		// Try to parse as date
		if t, err := time.ParseInLocation("2006-01-02", sinceStr, now.Location()); err == nil {
			start = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
		} else {
			fmt.Fprintf(os.Stderr, "%sWarning:%s Invalid date '%s', using today%s\n", colorYellow, colorReset, sinceStr, colorReset)
			start = startOfToday
		}
	}

	// Calculate end date (start of the day after the last included day)
	end = start.AddDate(0, 0, days)

	// Don't go beyond now
	if end.After(now) {
		end = now
	}

	// Build label
	if days == 1 {
		if start.Equal(startOfToday) {
			label = "today"
		} else if start.Equal(startOfToday.AddDate(0, 0, -1)) {
			label = "yesterday"
		} else {
			label = start.Format("Mon Jan 2")
		}
	} else {
		endDay := start.AddDate(0, 0, days-1) // Last included day
		if endDay.After(startOfToday) {
			endDay = startOfToday
		}
		label = fmt.Sprintf("%s to %s (%d days)", start.Format("Jan 2"), endDay.Format("Jan 2"), days)
	}

	return start, end, label
}

func runStats(start, end time.Time, label string) {
	// Open database directly
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sError:%s Failed to get home directory: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	dbPath := filepath.Join(homeDir, ".config", "overseer", "overseer.db")
	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sError:%s Failed to open database: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}
	defer database.Close()

	// Load config to get location names for IPs
	configPath := filepath.Join(homeDir, ".config", "overseer", "config.hcl")
	config, _ := core.LoadConfig(configPath) // Ignore error - location names are optional

	// Get online and IP sensor changes
	onlineChanges, ipChanges, err := getSensorChanges(database, start, end)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sError:%s Failed to query database: %v\n", colorRed, colorReset, err)
		os.Exit(1)
	}

	if len(onlineChanges) == 0 {
		fmt.Printf("%sNo online/offline events found%s\n", colorGray, colorReset)
		return
	}

	// Parse into sessions with IP tracking
	sessions := parseOnlineSessions(onlineChanges, ipChanges, start, end)

	// Print header
	fmt.Printf("%s%sConnectivity Statistics%s (%s)\n\n", colorBold, colorCyan, colorReset, label)

	// Print overall summary
	printSummary(sessions, start, end)

	// Group sessions by IP and print per-network stats
	ipStats := groupSessionsByIP(sessions, start, end, config)
	if len(ipStats) > 0 {
		fmt.Printf("\n%s%sNetwork Quality by IP:%s\n", colorBold, colorWhite, colorReset)
		printIPStats(ipStats, start, end)
	}

	// Print sessions grouped by day
	fmt.Printf("\n%s%sOnline Sessions:%s\n", colorBold, colorWhite, colorReset)
	printSessions(sessions)

	// Print overall network quality assessment
	fmt.Println()
	printNetworkQuality(sessions, start, end)
}

// getSensorChanges queries the database for online and IP sensor changes within a date range
func getSensorChanges(database *db.DB, start, end time.Time) (online, ip []db.SensorChange, err error) {
	// Get all sensor changes and filter
	allChanges, err := database.GetRecentSensorChanges(10000)
	if err != nil {
		return nil, nil, err
	}

	// Changes come in descending order (most recent first)
	// First pass: find the first offline event after query end (to know when ongoing sessions end)
	var firstOfflineAfterEnd *db.SensorChange
	for _, c := range allChanges {
		if c.SensorName == "online" && !c.Timestamp.Before(end) {
			if c.NewValue == "false" {
				cc := c // Copy to avoid pointer issues
				firstOfflineAfterEnd = &cc
				// Keep looking - we want the earliest offline after end
			}
		}
	}

	var onlineChanges, ipChanges []db.SensorChange
	for _, c := range allChanges {
		if c.SensorName == "online" {
			if c.Timestamp.Before(end) {
				onlineChanges = append(onlineChanges, c)
			}
		} else if c.SensorName == "public_ipv4" {
			if c.Timestamp.Before(end) {
				ipChanges = append(ipChanges, c)
			}
		}
	}

	// If there's an ongoing session at query end, include the offline event that ends it
	if firstOfflineAfterEnd != nil && len(onlineChanges) > 0 {
		// Check if the most recent event before end is "online" (session ongoing)
		if onlineChanges[0].NewValue == "true" {
			onlineChanges = append([]db.SensorChange{*firstOfflineAfterEnd}, onlineChanges...)
		}
	}

	// Reverse to get chronological order
	for i, j := 0, len(onlineChanges)-1; i < j; i, j = i+1, j-1 {
		onlineChanges[i], onlineChanges[j] = onlineChanges[j], onlineChanges[i]
	}
	for i, j := 0, len(ipChanges)-1; i < j; i, j = i+1, j-1 {
		ipChanges[i], ipChanges[j] = ipChanges[j], ipChanges[i]
	}
	return onlineChanges, ipChanges, nil
}

// getIPForSession finds the IP that was active during a session
// It looks at all IP changes during the session and returns the most recent valid one
func getIPForSession(ipChanges []db.SensorChange, sessionStart, sessionEnd time.Time) string {
	var lastIP string

	for _, c := range ipChanges {
		// Stop if we're past the session end
		if c.Timestamp.After(sessionEnd) {
			break
		}

		// Track any valid IP that occurred before or during the session
		// This includes IPs from before the session (as baseline) and during the session
		if c.NewValue == "169.254.0.0" || c.NewValue == "0.0.0.0" || c.NewValue == "" {
			// IP was cleared (e.g. at sleep) or is link-local — reset to unknown
			lastIP = ""
		} else {
			lastIP = c.NewValue
		}
	}

	if lastIP == "" {
		return "unknown"
	}
	return lastIP
}

// splitSessionsByIP takes sessions and splits any session where the public IP
// changed mid-session into multiple sub-sessions, one per IP.
func splitSessionsByIP(sessions []OnlineSession, ipChanges []db.SensorChange) []OnlineSession {
	var result []OnlineSession

	for _, s := range sessions {
		// Find the IP active at session start (last valid IP at or before s.Start)
		currentIP := "unknown"
		for _, c := range ipChanges {
			if c.Timestamp.After(s.Start) {
				break
			}
			if c.NewValue == "169.254.0.0" || c.NewValue == "0.0.0.0" || c.NewValue == "" {
				currentIP = "unknown"
			} else {
				currentIP = c.NewValue
			}
		}

		// Walk through IP changes strictly within the session, splitting at each one
		segStart := s.Start
		for _, c := range ipChanges {
			if !c.Timestamp.After(s.Start) {
				continue
			}
			if !c.Timestamp.Before(s.End) {
				break
			}
			if c.NewValue == "169.254.0.0" || c.NewValue == "0.0.0.0" || c.NewValue == "" {
				continue
			}

			// Close current segment and start a new one
			if c.Timestamp.After(segStart) {
				result = append(result, OnlineSession{
					Start:    segStart,
					End:      c.Timestamp,
					Duration: c.Timestamp.Sub(segStart),
					IP:       currentIP,
				})
			}
			segStart = c.Timestamp
			currentIP = c.NewValue
		}

		// Final segment
		if s.End.After(segStart) {
			result = append(result, OnlineSession{
				Start:    segStart,
				End:      s.End,
				Duration: s.End.Sub(segStart),
				IP:       currentIP,
			})
		}
	}

	return result
}

// parseOnlineSessions converts online/offline events into sessions with IP tracking
func parseOnlineSessions(onlineChanges, ipChanges []db.SensorChange, start, end time.Time) []OnlineSession {
	var sessions []OnlineSession
	var sessionStart time.Time
	inSession := false

	for _, c := range onlineChanges {
		// Track state changes before our start time to know if we're in a session
		if c.Timestamp.Before(start) {
			if c.NewValue == "true" {
				inSession = true
				sessionStart = c.Timestamp // Preserve actual start time
			} else {
				inSession = false
			}
			continue
		}

		if c.NewValue == "true" && !inSession {
			// Going online - start session
			sessionStart = c.Timestamp
			inSession = true
		} else if c.NewValue == "false" && inSession {
			// Going offline - end session
			// Only include if session overlaps with query period
			if !c.Timestamp.Before(start) {
				ip := getIPForSession(ipChanges, sessionStart, c.Timestamp)
				sessions = append(sessions, OnlineSession{
					Start:    sessionStart,
					End:      c.Timestamp,
					Duration: c.Timestamp.Sub(sessionStart),
					IP:       ip,
				})
			}
			inSession = false
		}
	}

	// If still online, add current session
	if inSession {
		sessionEnd := end
		if end.After(time.Now()) {
			sessionEnd = time.Now()
		}
		ip := getIPForSession(ipChanges, sessionStart, sessionEnd)
		sessions = append(sessions, OnlineSession{
			Start:    sessionStart,
			End:      sessionEnd,
			Duration: sessionEnd.Sub(sessionStart),
			IP:       ip,
		})
	}

	// Split sessions at IP change boundaries so each sub-session has one IP
	sessions = splitSessionsByIP(sessions, ipChanges)

	// Drop brief unknown-IP segments (wake-up artifacts before IP probe runs)
	filtered := sessions[:0]
	for _, s := range sessions {
		if s.IP == "unknown" && s.Duration < time.Minute {
			continue
		}
		filtered = append(filtered, s)
	}

	if len(filtered) == 0 {
		return filtered
	}

	// Merge adjacent sessions with the same IP and touching timestamps
	merged := []OnlineSession{filtered[0]}
	for _, s := range filtered[1:] {
		last := &merged[len(merged)-1]
		if last.End.Equal(s.Start) && last.IP == s.IP {
			last.End = s.End
			last.Duration = last.End.Sub(last.Start)
		} else {
			merged = append(merged, s)
		}
	}
	return merged
}

// groupSessionsByIP groups sessions by their IP address
func groupSessionsByIP(sessions []OnlineSession, start, end time.Time, config *core.Configuration) []IPStats {
	ipMap := make(map[string]*IPStats)

	for _, s := range sessions {
		ip := s.IP
		if ip == "" {
			ip = "unknown"
		}

		stats, exists := ipMap[ip]
		if !exists {
			stats = &IPStats{
				IP:           ip,
				LocationName: getLocationForIP(ip, config),
			}
			ipMap[ip] = stats
		}

		// Clip session to query period for duration calculation
		sessionStart := s.Start
		sessionEnd := s.End
		if sessionStart.Before(start) {
			sessionStart = start
		}
		if sessionEnd.After(end) {
			sessionEnd = end
		}
		clippedDuration := time.Duration(0)
		if sessionEnd.After(sessionStart) {
			clippedDuration = sessionEnd.Sub(sessionStart)
		}

		stats.Sessions = append(stats.Sessions, s)
		stats.TotalOnline += clippedDuration
		stats.SessionCount++
		if clippedDuration < 5*time.Minute {
			stats.ShortSessions++
		}
	}

	// Convert map to slice and sort by total online time (descending)
	var result []IPStats
	for _, stats := range ipMap {
		result = append(result, *stats)
	}

	// Sort by total online time descending
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].TotalOnline > result[i].TotalOnline {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

// countMaxConsecutiveShort returns the maximum streak of consecutive short sessions
func countMaxConsecutiveShort(sessions []OnlineSession) int {
	if len(sessions) == 0 {
		return 0
	}

	maxStreak := 0
	currentStreak := 0

	for _, s := range sessions {
		if s.Duration < 5*time.Minute {
			currentStreak++
			if currentStreak > maxStreak {
				maxStreak = currentStreak
			}
		} else {
			currentStreak = 0
		}
	}

	return maxStreak
}

// assessIPQuality determines the quality rating for a network based on session patterns
// Returns quality label, color, and any issues detected
func assessIPQuality(stats IPStats) (quality, qualityColor string, issues []string) {
	// Calculate base metrics
	avgDuration := time.Duration(0)
	if stats.SessionCount > 0 {
		avgDuration = stats.TotalOnline / time.Duration(stats.SessionCount)
	}

	// Count consecutive short sessions - the clearest instability indicator
	maxConsecutiveShort := countMaxConsecutiveShort(stats.Sessions)

	// Calculate reconnect rate per hour of online time
	// (more meaningful than per calendar day)
	reconnectsPerHour := float64(0)
	if stats.TotalOnline >= 30*time.Minute {
		// Subtract 1 because the first connection isn't a "reconnect"
		reconnects := stats.SessionCount - 1
		if reconnects > 0 {
			reconnectsPerHour = float64(reconnects) / stats.TotalOnline.Hours()
		}
	}

	// === SINGLE SESSION ===
	if stats.SessionCount == 1 {
		// For single session, use full duration (not clipped) for quality assessment
		singleSessionDuration := avgDuration
		if len(stats.Sessions) == 1 {
			singleSessionDuration = stats.Sessions[0].Duration
		}
		if singleSessionDuration >= 4*time.Hour {
			return "Excellent", colorBoldGreen, nil
		}
		if singleSessionDuration >= 1*time.Hour {
			return "Stable", colorGreen, nil
		}
		if singleSessionDuration >= 10*time.Minute {
			return "New", colorWhite, nil
		}
		return "New", colorGray, nil
	}

	// === POOR - Clear instability patterns ===

	// Multiple consecutive short sessions is definitive instability
	if maxConsecutiveShort >= 3 {
		issues = append(issues, fmt.Sprintf("%d consecutive brief sessions", maxConsecutiveShort))
		return "Poor", colorBoldRed, issues
	}

	// High reconnect rate with meaningful sample size
	if stats.SessionCount >= 4 && reconnectsPerHour > 2 {
		issues = append(issues, fmt.Sprintf("High reconnect rate (%.1f/hr)", reconnectsPerHour))
		return "Poor", colorBoldRed, issues
	}

	// Many short sessions (absolute count, not percentage)
	if stats.ShortSessions >= 4 {
		issues = append(issues, fmt.Sprintf("%d brief sessions", stats.ShortSessions))
		return "Poor", colorBoldRed, issues
	}

	// === EXCELLENT - Very stable ===
	if maxConsecutiveShort == 0 &&
		stats.ShortSessions <= 1 &&
		avgDuration >= 30*time.Minute {
		return "Excellent", colorBoldGreen, nil
	}

	// === GOOD - Mostly stable ===
	if maxConsecutiveShort <= 1 &&
		stats.ShortSessions <= 2 &&
		avgDuration >= 10*time.Minute {
		return "Good", colorGreen, nil
	}

	// === FAIR - Some issues but not terrible ===
	if maxConsecutiveShort >= 2 {
		issues = append(issues, fmt.Sprintf("%d consecutive brief sessions", maxConsecutiveShort))
	}
	if stats.ShortSessions >= 3 {
		issues = append(issues, fmt.Sprintf("%d brief sessions", stats.ShortSessions))
	}
	if reconnectsPerHour > 1.5 && stats.SessionCount >= 3 {
		issues = append(issues, fmt.Sprintf("Frequent reconnects (%.1f/hr)", reconnectsPerHour))
	}

	return "Fair", colorYellow, issues
}

// printIPStats prints statistics for each IP/network
func printIPStats(ipStats []IPStats, start, end time.Time) {
	for _, stats := range ipStats {
		// Calculate average duration for display
		avgDuration := time.Duration(0)
		if stats.SessionCount > 0 {
			avgDuration = stats.TotalOnline / time.Duration(stats.SessionCount)
		}

		// Assess quality using the new logic
		quality, qualityColor, issues := assessIPQuality(stats)

		// Print IP header with quality dot and optional location name
		if stats.LocationName != "" {
			fmt.Printf("\n  %s●%s %s%s%s %s%s%s %s%s%s\n",
				qualityColor, colorReset,
				colorBold, stats.LocationName, colorReset,
				getIPColor(stats.IP), stats.IP, colorReset,
				qualityColor, quality, colorReset)
		} else {
			fmt.Printf("\n  %s●%s %s%s%s %s%s%s\n",
				qualityColor, colorReset,
				getIPColor(stats.IP), stats.IP, colorReset,
				qualityColor, quality, colorReset)
		}

		// Print stats (use IP's color for consistency)
		ipColor := getIPColor(stats.IP)
		fmt.Printf("    Online: %s%s%s  Sessions: %s%d%s  Avg: %s%s%s\n",
			colorGreen, formatDuration(stats.TotalOnline), colorReset,
			ipColor, stats.SessionCount, colorReset,
			ipColor, formatDuration(avgDuration), colorReset)

		// Print any issues detected
		if len(issues) > 0 {
			for _, issue := range issues {
				fmt.Printf("    %s⚠ %s%s\n", colorYellow, issue, colorReset)
			}
		}
	}
}

func printSummary(sessions []OnlineSession, start, end time.Time) {
	// Calculate total online time by merging overlapping periods within query range
	// This ensures we never exceed the query period duration
	type timeRange struct{ start, end time.Time }
	var ranges []timeRange

	for _, s := range sessions {
		// Clip session to query period
		sessionStart := s.Start
		sessionEnd := s.End
		if sessionStart.Before(start) {
			sessionStart = start
		}
		if sessionEnd.After(end) {
			sessionEnd = end
		}
		if sessionEnd.After(sessionStart) {
			ranges = append(ranges, timeRange{sessionStart, sessionEnd})
		}
	}

	// Sort ranges by start time
	for i := 0; i < len(ranges)-1; i++ {
		for j := i + 1; j < len(ranges); j++ {
			if ranges[i].start.After(ranges[j].start) {
				ranges[i], ranges[j] = ranges[j], ranges[i]
			}
		}
	}

	// Merge overlapping ranges and calculate total
	var totalOnline time.Duration
	var merged []timeRange
	for _, r := range ranges {
		if len(merged) == 0 || r.start.After(merged[len(merged)-1].end) {
			merged = append(merged, r)
		} else if r.end.After(merged[len(merged)-1].end) {
			merged[len(merged)-1].end = r.end
		}
	}
	for _, r := range merged {
		totalOnline += r.end.Sub(r.start)
	}

	// Cap at query period duration (safety check)
	maxDuration := end.Sub(start)
	if end.After(time.Now()) {
		maxDuration = time.Now().Sub(start)
	}
	if totalOnline > maxDuration {
		totalOnline = maxDuration
	}

	avgSessionDuration := time.Duration(0)
	if len(sessions) > 0 {
		avgSessionDuration = totalOnline / time.Duration(len(sessions))
	}

	// Print summary box
	fmt.Printf("%sSummary:%s\n", colorBold, colorReset)
	fmt.Printf("  Total Online Time:    %s%s%s\n", colorGreen, formatDuration(totalOnline), colorReset)
	fmt.Printf("  Sessions:             %s%d%s\n", colorWhite, len(sessions), colorReset)
	fmt.Printf("  Avg Session Duration: %s%s%s\n", colorWhite, formatDuration(avgSessionDuration), colorReset)
}

// sessionEntry represents a session or part of a session for display in a day
type sessionEntry struct {
	session       OnlineSession
	displayStart  time.Time
	displayEnd    time.Time
	continuesNext bool // Session continues to next day
	continuesPrev bool // Session continued from previous day
	isActive      bool
}

func printSessions(sessions []OnlineSession) {
	if len(sessions) == 0 {
		fmt.Printf("  %s(no sessions)%s\n", colorGray, colorReset)
		return
	}

	// Build display entries, splitting sessions that cross day boundaries
	type dayGroup struct {
		date    string
		entries []sessionEntry
	}
	dayMap := make(map[string]*dayGroup)

	for _, s := range sessions {
		startLocal := s.Start.Local()
		endLocal := s.End.Local()

		// Check if session ends exactly at midnight (capped at query end)
		midnight := time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 0, 0, 0, 0, endLocal.Location())
		endsAtMidnight := endLocal.Equal(midnight) && endLocal.After(startLocal)

		// If session ends exactly at midnight, adjust to 23:59:59 of previous day for display
		if endsAtMidnight {
			endLocal = midnight.Add(-time.Second)
		}

		startDate := startLocal.Format("2006-01-02")
		endDate := endLocal.Format("2006-01-02")
		isActive := time.Since(s.End) < time.Second

		if startDate == endDate {
			// Session within same day (or adjusted from midnight)
			if dayMap[startDate] == nil {
				dayMap[startDate] = &dayGroup{date: startDate}
			}
			dayMap[startDate].entries = append(dayMap[startDate].entries, sessionEntry{
				session:       s,
				displayStart:  startLocal,
				displayEnd:    endLocal,
				continuesNext: endsAtMidnight, // Mark as continuing if it was capped at midnight
				isActive:      isActive,
			})
		} else {
			// Session crosses day boundary - add entry for start day
			startDayEnd := time.Date(startLocal.Year(), startLocal.Month(), startLocal.Day(), 23, 59, 59, 0, startLocal.Location())
			if dayMap[startDate] == nil {
				dayMap[startDate] = &dayGroup{date: startDate}
			}
			dayMap[startDate].entries = append(dayMap[startDate].entries, sessionEntry{
				session:       s,
				displayStart:  startLocal,
				displayEnd:    startDayEnd,
				continuesNext: true,
			})

			// Add entry for end day (skip if session ends exactly at midnight - 0 duration on that day)
			endDayStart := time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 0, 0, 0, 0, endLocal.Location())
			if endLocal.After(endDayStart) {
				if dayMap[endDate] == nil {
					dayMap[endDate] = &dayGroup{date: endDate}
				}
				dayMap[endDate].entries = append(dayMap[endDate].entries, sessionEntry{
					session:       s,
					displayStart:  endDayStart,
					displayEnd:    endLocal,
					continuesPrev: true,
					isActive:      isActive,
				})
			}
		}
	}

	// Sort days
	var dates []string
	for date := range dayMap {
		dates = append(dates, date)
	}
	for i := 0; i < len(dates)-1; i++ {
		for j := i + 1; j < len(dates); j++ {
			if dates[i] > dates[j] {
				dates[i], dates[j] = dates[j], dates[i]
			}
		}
	}

	// Print each day's sessions
	for _, dateStr := range dates {
		g := dayMap[dateStr]

		// Sort entries by start time
		entries := g.entries
		for i := 0; i < len(entries)-1; i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[i].displayStart.After(entries[j].displayStart) {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}

		// Parse date for day name
		date, _ := time.Parse("2006-01-02", g.date)
		dayName := date.Format("Mon")

		// Calculate day's total online time (only count actual time in this day)
		var dayTotal time.Duration
		for _, e := range entries {
			dayTotal += e.displayEnd.Sub(e.displayStart)
		}

		// Day header with color based on whether it's today
		dayColor := colorBlue
		if g.date == time.Now().Format("2006-01-02") {
			dayColor = colorBoldGreen
		}
		fmt.Printf("\n  %s%s %s%s %s(%s total, %d entries)%s\n",
			dayColor, dayName, g.date, colorReset,
			colorGray, formatDuration(dayTotal), len(entries), colorReset)

		// Print entries for this day (oldest first)
		for j := 0; j < len(entries); j++ {
			e := entries[j]
			s := e.session

			// Format times with continuation indicators
			var startTime, endTime string
			if e.continuesPrev {
				// Show actual start time from previous day in gray
				actualStart := s.Start.Local()
				startTime = fmt.Sprintf("%s%s%s", colorGray, actualStart.Format("15:04:05"), colorReset)
			} else {
				startTime = e.displayStart.Format("15:04:05")
			}

			if e.continuesNext {
				// Show actual end time in gray (if we have it, not capped at midnight)
				actualEnd := s.End.Local()
				midnight := time.Date(actualEnd.Year(), actualEnd.Month(), actualEnd.Day(), 0, 0, 0, 0, actualEnd.Location())
				if actualEnd.Equal(midnight) {
					// Session was capped at midnight - show end of day
					endTime = e.displayEnd.Format("15:04:05")
				} else {
					// We have the actual end time on the next day
					endTime = fmt.Sprintf("%s%s%s", colorGray, actualEnd.Format("15:04:05"), colorReset)
				}
			} else if e.isActive {
				endTime = fmt.Sprintf("%snow%s", colorGreen, colorReset)
			} else {
				endTime = e.displayEnd.Format("15:04:05")
			}

			// Calculate duration for this day's portion
			entryDuration := e.displayEnd.Sub(e.displayStart)
			durationColor := sessionDurationColor(entryDuration)

			// Format IP
			ipStr := ""
			if s.IP != "" && s.IP != "unknown" {
				ipStr = fmt.Sprintf(" %s[%s]%s", getIPColor(s.IP), s.IP, colorReset)
			}

			// Choose indicator
			indicator := fmt.Sprintf("%s○%s", colorGray, colorReset)
			if e.isActive {
				indicator = fmt.Sprintf("%s●%s", colorGreen, colorReset)
			} else if e.continuesPrev || e.continuesNext {
				indicator = fmt.Sprintf("%s◐%s", colorBlue, colorReset)
			}

			fmt.Printf("    %s %s - %s  %s%s%s%s",
				indicator,
				startTime, endTime,
				durationColor, formatDuration(entryDuration), colorReset,
				ipStr)

			if e.isActive {
				fmt.Printf(" %s(active)%s", colorGreen, colorReset)
			}
			fmt.Println()
		}
	}
}

func printNetworkQuality(sessions []OnlineSession, start, end time.Time) {
	if len(sessions) == 0 {
		return
	}

	// Calculate metrics for quality assessment (clipped to query period)
	var totalOnline time.Duration
	var shortSessions int // Sessions < 5 minutes
	clippedSessions := make([]OnlineSession, 0, len(sessions))

	for _, s := range sessions {
		// Clip session to query period
		sessionStart := s.Start
		sessionEnd := s.End
		if sessionStart.Before(start) {
			sessionStart = start
		}
		if sessionEnd.After(end) {
			sessionEnd = end
		}
		clippedDuration := time.Duration(0)
		if sessionEnd.After(sessionStart) {
			clippedDuration = sessionEnd.Sub(sessionStart)
		}
		totalOnline += clippedDuration
		if clippedDuration < 5*time.Minute {
			shortSessions++
		}
		// Store clipped session for consecutive analysis
		clippedSessions = append(clippedSessions, OnlineSession{
			Start:    sessionStart,
			End:      sessionEnd,
			Duration: clippedDuration,
			IP:       s.IP,
		})
	}

	sessionCount := len(sessions)
	avgDuration := totalOnline / time.Duration(sessionCount)

	// For single session quality assessment, use the FULL session duration
	// (not clipped to query period) to properly assess ongoing stability.
	// A 7-hour session that crosses midnight should be rated based on its
	// actual duration, not just the portion within "today".
	if sessionCount == 1 && len(sessions) == 1 {
		avgDuration = sessions[0].Duration
	}

	// Count consecutive short sessions
	maxConsecutiveShort := countMaxConsecutiveShort(clippedSessions)

	// Calculate reconnect rate per hour of online time
	reconnectsPerHour := float64(0)
	if totalOnline >= 30*time.Minute {
		reconnects := sessionCount - 1
		if reconnects > 0 {
			reconnectsPerHour = float64(reconnects) / totalOnline.Hours()
		}
	}

	// Determine quality level based on session stability
	var quality string
	var qualityColor string
	var issues []string

	// === SINGLE SESSION ===
	if sessionCount == 1 {
		if avgDuration >= 4*time.Hour {
			// 4+ hours without disconnection is excellent
			quality = "Excellent"
			qualityColor = colorBoldGreen
		} else if avgDuration >= 1*time.Hour {
			quality = "Stable"
			qualityColor = colorGreen
		} else if avgDuration >= 10*time.Minute {
			quality = "New"
			qualityColor = colorWhite
		} else {
			quality = "New"
			qualityColor = colorGray
		}
		fmt.Printf("%s%sOverall Network Quality:%s %s%s%s\n", colorBold, colorWhite, colorReset, qualityColor, quality, colorReset)
		return
	}

	// === POOR - Clear instability patterns ===

	// Multiple consecutive short sessions is definitive instability
	if maxConsecutiveShort >= 3 {
		issues = append(issues, fmt.Sprintf("%d consecutive brief sessions", maxConsecutiveShort))
		quality = "Poor"
		qualityColor = colorBoldRed
	} else if sessionCount >= 4 && reconnectsPerHour > 2 {
		// High reconnect rate with meaningful sample
		issues = append(issues, fmt.Sprintf("High reconnect rate (%.1f/hr)", reconnectsPerHour))
		quality = "Poor"
		qualityColor = colorBoldRed
	} else if shortSessions >= 4 {
		// Many short sessions (absolute count)
		issues = append(issues, fmt.Sprintf("%d brief sessions", shortSessions))
		quality = "Poor"
		qualityColor = colorBoldRed
	} else if maxConsecutiveShort == 0 && shortSessions <= 1 && avgDuration >= 30*time.Minute {
		// === EXCELLENT - Very stable ===
		quality = "Excellent"
		qualityColor = colorBoldGreen
	} else if maxConsecutiveShort <= 1 && shortSessions <= 2 && avgDuration >= 10*time.Minute {
		// === GOOD - Mostly stable ===
		quality = "Good"
		qualityColor = colorGreen
	} else {
		// === FAIR - Some issues but not terrible ===
		quality = "Fair"
		qualityColor = colorYellow

		// Collect warnings for Fair rating
		if maxConsecutiveShort >= 2 {
			issues = append(issues, fmt.Sprintf("%d consecutive brief sessions", maxConsecutiveShort))
		}
		if shortSessions >= 3 {
			issues = append(issues, fmt.Sprintf("%d brief sessions", shortSessions))
		}
		if reconnectsPerHour > 1.5 && sessionCount >= 3 {
			issues = append(issues, fmt.Sprintf("Frequent reconnects (%.1f/hr)", reconnectsPerHour))
		}
	}

	fmt.Printf("%s%sOverall Network Quality:%s %s%s%s\n", colorBold, colorWhite, colorReset, qualityColor, quality, colorReset)

	if len(issues) > 0 {
		fmt.Printf("  %sIssues detected:%s\n", colorYellow, colorReset)
		for _, issue := range issues {
			fmt.Printf("    %s⚠%s %s\n", colorYellow, colorReset, issue)
		}
	}
}

// Helper functions

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		if secs > 0 {
			return fmt.Sprintf("%dm%ds", mins, secs)
		}
		return fmt.Sprintf("%dm", mins)
	} else if d < 24*time.Hour {
		hours := int(d.Hours())
		mins := int(d.Minutes()) % 60
		if mins > 0 {
			return fmt.Sprintf("%dh%dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if hours > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	return fmt.Sprintf("%dd", days)
}

func sessionDurationColor(d time.Duration) string {
	if d < time.Minute {
		return colorRed // Very short - likely connection issue
	} else if d < 5*time.Minute {
		return colorYellow // Short session
	} else if d < time.Hour {
		return colorWhite // Normal session
	}
	return colorGreen // Long stable session
}
