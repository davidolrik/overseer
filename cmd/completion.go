// in cmd/completion.go
package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.olrik.dev/overseer/internal/core"
)

// extractHostAliases is a simple, robust parser that only looks for the `Host` keyword
// and extracts the alias(es) that follow it on the same line.
// It safely ignores all other directives
func extractHostAliases(fullConfig string) []string {
	var hosts []string
	seen := make(map[string]bool)
	lines := strings.Split(fullConfig, "\n")

	for _, line := range lines {
		fields := strings.Fields(line)

		if len(fields) < 2 {
			continue
		}

		if strings.EqualFold(fields[0], "Host") {
			for _, alias := range fields[1:] {
				if strings.HasPrefix(alias, "#") {
					break // Stop processing on this line if a comment is found.
				}

				// Ignore any alias that contains a wildcard character (* or ?).
				if strings.ContainsAny(alias, "*?") {
					continue
				}

				if !seen[alias] {
					hosts = append(hosts, alias)
					seen[alias] = true
				}
			}
		}
	}
	return hosts
}

// recursivelyReadAllSSHConfigs reads a root config file, follows all `Include` directives,
// and returns the concatenated content as a single string.
func recursivelyReadAllSSHConfigs(path string, visited map[string]bool) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if visited[absPath] {
		return "", nil // Cycle detected
	}
	visited[absPath] = true

	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var finalConfig bytes.Buffer
	finalConfig.Write(content)
	finalConfig.WriteString("\n")

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if len(trimmedLine) > 7 && strings.EqualFold(trimmedLine[:7], "include") {
			parts := strings.Fields(trimmedLine)
			if len(parts) < 2 {
				continue
			}
			includePathPattern := parts[1]
			expandedPath := includePathPattern
			if strings.HasPrefix(includePathPattern, "~/") {
				homeDir, err := os.UserHomeDir()
				if err != nil {
					continue
				}
				expandedPath = filepath.Join(homeDir, includePathPattern[2:])
			} else if !filepath.IsAbs(includePathPattern) {
				expandedPath = filepath.Join(filepath.Dir(path), includePathPattern)
			}
			matches, err := filepath.Glob(expandedPath)
			if err != nil {
				continue
			}
			for _, match := range matches {
				includedContent, err := recursivelyReadAllSSHConfigs(match, visited)
				if err == nil {
					finalConfig.WriteString(includedContent)
				}
			}
		}
	}
	return finalConfig.String(), nil
}

func sshHostCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	sshConfigFile := filepath.Join(homeDir, ".ssh", "config")

	// 1. Recursively read all config files into a single string.
	fullConfigString, err := recursivelyReadAllSSHConfigs(sshConfigFile, make(map[string]bool))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// 2. Use our new, safe extractor to get only the host aliases.
	// This function CANNOT fail on `Match` directives or cause a panic.
	hosts := extractHostAliases(fullConfigString)

	// 3. Sort and return the results.
	sort.Strings(hosts)
	return hosts, cobra.ShellCompDirectiveNoFileComp
}

// tunnelCompletionFunc returns configured tunnel aliases from overseer config
func tunnelCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	tunnels := getConfiguredTunnels()
	sort.Strings(tunnels)
	return tunnels, cobra.ShellCompDirectiveNoFileComp
}

// companionCompletionFunc returns companion names for the tunnel specified by --tunnel flag
func companionCompletionFunc(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	tunnel, _ := cmd.Flags().GetString("tunnel")
	if tunnel == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	companions := getConfiguredCompanions(tunnel)
	sort.Strings(companions)
	return companions, cobra.ShellCompDirectiveNoFileComp
}

// getConfiguredTunnels returns all tunnel aliases from the overseer config
func getConfiguredTunnels() []string {
	if core.Config == nil || core.Config.Tunnels == nil {
		return nil
	}

	tunnels := make([]string, 0, len(core.Config.Tunnels))
	for alias := range core.Config.Tunnels {
		tunnels = append(tunnels, alias)
	}
	return tunnels
}

// getConfiguredCompanions returns all companion names for a given tunnel
func getConfiguredCompanions(tunnel string) []string {
	if core.Config == nil || core.Config.Tunnels == nil {
		return nil
	}

	tunnelConfig, exists := core.Config.Tunnels[tunnel]
	if !exists || tunnelConfig == nil {
		return nil
	}

	companions := make([]string, 0, len(tunnelConfig.Companions))
	for _, comp := range tunnelConfig.Companions {
		companions = append(companions, comp.Name)
	}
	return companions
}
