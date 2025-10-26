package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExportWriter handles writing security context data to a file in various formats
type ExportWriter struct {
	exportType string // Export type: "dotenv", "context", "location", "public_ip"
	path       string // Path to the output file
}

// NewExportWriter creates a new export writer for the given type and path
func NewExportWriter(exportType, path string) (*ExportWriter, error) {
	if path == "" {
		return nil, fmt.Errorf("output file path cannot be empty")
	}

	// Validate export type
	validTypes := map[string]bool{
		"dotenv":    true,
		"context":   true,
		"location":  true,
		"public_ip": true,
	}
	if !validTypes[exportType] {
		return nil, fmt.Errorf("invalid export type: %s", exportType)
	}

	// Resolve path (handle ~, relative paths, etc.)
	absPath, err := resolveFilePath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	return &ExportWriter{
		exportType: exportType,
		path:       absPath,
	}, nil
}

// ExportData contains all the data that can be exported
type ExportData struct {
	Context              string
	ContextDisplayName   string
	Location             string
	LocationDisplayName  string
	PublicIP             string
	CustomEnvironment    map[string]string // Custom environment variables from context and location
}

// Write writes the export data to the file atomically based on the export type
func (ew *ExportWriter) Write(data ExportData) error {
	var content string

	switch ew.exportType {
	case "dotenv":
		// Create env-style file with OVERSEER_ prefix
		var lines []string
		if data.Context != "" {
			lines = append(lines, fmt.Sprintf("OVERSEER_CONTEXT=\"%s\"", data.Context))
		}
		if data.ContextDisplayName != "" {
			lines = append(lines, fmt.Sprintf("OVERSEER_CONTEXT_DISPLAY_NAME=\"%s\"", data.ContextDisplayName))
		}
		if data.Location != "" {
			lines = append(lines, fmt.Sprintf("OVERSEER_LOCATION=\"%s\"", data.Location))
		}
		if data.LocationDisplayName != "" {
			lines = append(lines, fmt.Sprintf("OVERSEER_LOCATION_DISPLAY_NAME=\"%s\"", data.LocationDisplayName))
		}
		if data.PublicIP != "" {
			lines = append(lines, fmt.Sprintf("OVERSEER_PUBLIC_IP=\"%s\"", data.PublicIP))
		}

		// Add custom environment variables
		if data.CustomEnvironment != nil {
			for key, value := range data.CustomEnvironment {
				lines = append(lines, fmt.Sprintf("%s=\"%s\"", key, value))
			}
		}

		content = strings.Join(lines, "\n") + "\n"

	case "context":
		content = data.Context + "\n"

	case "location":
		content = data.Location + "\n"

	case "public_ip":
		content = data.PublicIP + "\n"

	default:
		return fmt.Errorf("unknown export type: %s", ew.exportType)
	}

	// Create temporary file in the same directory
	tempFile := ew.path + ".tmp"

	// Write to temporary file
	if err := os.WriteFile(tempFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempFile, ew.path); err != nil {
		// Clean up temp file on error
		os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// GetPath returns the absolute path to the output file
func (ew *ExportWriter) GetPath() string {
	return ew.path
}

// GetType returns the export type
func (ew *ExportWriter) GetType() string {
	return ew.exportType
}

// resolveFilePath resolves a file path, handling ~ and making it absolute
func resolveFilePath(path string) (string, error) {
	// Expand ~ to home directory
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}

	// Make absolute
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	return absPath, nil
}
