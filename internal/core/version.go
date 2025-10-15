package core

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:generate sh -c "(git describe --tags --long --dirty='-devel' --match '[0-9]*.[0-9]*.[0-9]*' || echo '0.0.0') > version.txt"
//go:embed version.txt
var version string

var Version string = strings.TrimSpace(version)

// FormatVersion formats the version string for nice display.
// Input format: "v0.7.0-5-g9154987-devel"
// Output examples:
//   - "0.7.0 (9154987)" - clean release
//   - "0.7.0+5 (9154987)" - 5 commits after tag
//   - "0.7.0+5 (9154987-devel)" - development version with uncommitted changes
func FormatVersion(v string) string {
	// Remove 'v' prefix if present
	v = strings.TrimPrefix(v, "v")

	// Split by '-' to parse components
	parts := strings.Split(v, "-")

	if len(parts) < 3 {
		// Fallback for unexpected format
		return v
	}

	baseVersion := parts[0]           // e.g., "0.7.0"
	commitsSinceTag := parts[1]       // e.g., "5" or "0"
	sha := strings.TrimPrefix(parts[2], "g") // e.g., "9154987" (remove 'g' prefix)

	// Check if this is a development version
	isDevel := len(parts) > 3 && parts[3] == "devel"

	// Build the formatted version
	result := baseVersion

	// Add commit count if not zero
	if commitsSinceTag != "0" {
		result += fmt.Sprintf("+%s", commitsSinceTag)
	}

	// Add SHA
	if isDevel {
		result += fmt.Sprintf(" (%s-devel)", sha)
	} else {
		result += fmt.Sprintf(" (%s)", sha)
	}

	return result
}
