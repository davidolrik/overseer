package security

import (
	"fmt"
	"os"
	"path/filepath"
)

// FileWriter handles writing the current context to a file
type FileWriter struct {
	path string // Path to the output file
}

// NewFileWriter creates a new file writer for the given path
func NewFileWriter(path string) (*FileWriter, error) {
	if path == "" {
		return nil, fmt.Errorf("output file path cannot be empty")
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

	return &FileWriter{
		path: absPath,
	}, nil
}

// Write writes the context name to the file atomically
func (fw *FileWriter) Write(contextName string) error {
	// Create temporary file in the same directory
	tempFile := fw.path + ".tmp"

	// Write to temporary file
	if err := os.WriteFile(tempFile, []byte(contextName+"\n"), 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempFile, fw.path); err != nil {
		// Clean up temp file on error
		os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// GetPath returns the absolute path to the output file
func (fw *FileWriter) GetPath() string {
	return fw.path
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
