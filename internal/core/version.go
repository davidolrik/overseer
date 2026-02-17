package core

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var Version string

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		Version = "devel"
		return
	}

	// Use module version for tagged releases (set by go install or goreleaser).
	// Skip pseudo-versions (local builds in Go 1.24+) — we use VCS info instead.
	if v := info.Main.Version; v != "" && v != "(devel)" && !isPseudoVersion(v) {
		Version = v
		return
	}

	// Fall back to VCS info for local builds
	var revision string
	var dirty bool

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}

	if revision == "" {
		Version = "devel"
		return
	}

	short := revision
	if len(short) > 7 {
		short = short[:7]
	}

	Version = fmt.Sprintf("devel-%s", short)
	if dirty {
		Version += "-dirty"
	}
}

// FormatVersion formats the version string for display.
// Tagged releases have the "v" prefix stripped; devel versions pass through as-is.
// Input/output examples:
//   - "v1.12.0" → "1.12.0"
//   - "devel-ad721b3" → "devel-ad721b3"
//   - "devel-ad721b3-dirty" → "devel-ad721b3-dirty"
//   - "devel" → "devel"
func FormatVersion(v string) string {
	return strings.TrimPrefix(v, "v")
}

// isPseudoVersion reports whether v looks like a Go module pseudo-version.
// Pseudo-versions end with a 12-character hex commit hash, e.g.
// v0.0.0-20260217105831-82903d1d8810 or v1.12.1-0.20260217105831-82903d1d8810.
func isPseudoVersion(v string) bool {
	// Strip build metadata (+dirty, +incompatible, etc.)
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	i := strings.LastIndex(v, "-")
	if i < 0 {
		return false
	}
	hash := v[i+1:]
	if len(hash) != 12 {
		return false
	}
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
