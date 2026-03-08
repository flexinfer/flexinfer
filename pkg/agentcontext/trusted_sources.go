package agentcontext

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TrustedSourceRegistry manages trusted source patterns
type TrustedSourceRegistry struct {
	sources []TrustedSource
}

// NewTrustedSourceRegistry creates a new registry
func NewTrustedSourceRegistry(sources []TrustedSource) *TrustedSourceRegistry {
	return &TrustedSourceRegistry{sources: sources}
}

// LoadFromEnv loads trusted sources from environment variable
// Format: pattern1:priority1:desc1;pattern2:priority2:desc2
func LoadTrustedSourcesFromEnv() []TrustedSource {
	envVal := os.Getenv("AGENT_CONTEXT_TRUSTED_SOURCES")
	if envVal == "" {
		return defaultTrustedSources()
	}

	var sources []TrustedSource
	entries := strings.Split(envVal, ";")
	for _, entry := range entries {
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) < 2 {
			continue
		}

		priority := 0.5
		if len(parts) >= 2 {
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil && parsed > 0 && parsed <= 1 {
				priority = parsed
			}
		}

		desc := ""
		if len(parts) >= 3 {
			desc = parts[2]
		}

		sources = append(sources, TrustedSource{
			Pattern:     parts[0],
			Priority:    priority,
			Description: desc,
		})
	}

	if len(sources) == 0 {
		return defaultTrustedSources()
	}
	return sources
}

// defaultTrustedSources returns sensible defaults for trusted sources
func defaultTrustedSources() []TrustedSource {
	return []TrustedSource{
		{Pattern: "*.md", Priority: 0.9, Description: "Documentation files"},
		{Pattern: "README*", Priority: 0.95, Description: "README files"},
		{Pattern: "AGENTS.md", Priority: 0.95, Description: "Agent instructions"},
		{Pattern: "CLAUDE.md", Priority: 0.95, Description: "Claude instructions"},
		{Pattern: "*_test.go", Priority: 0.8, Description: "Go test files"},
		{Pattern: "*_test.py", Priority: 0.8, Description: "Python test files"},
		{Pattern: "*.spec.ts", Priority: 0.8, Description: "TypeScript test files"},
		{Pattern: "go.mod", Priority: 0.85, Description: "Go module file"},
		{Pattern: "package.json", Priority: 0.85, Description: "Node.js package file"},
		{Pattern: "Makefile", Priority: 0.8, Description: "Build file"},
		{Pattern: ".env.example", Priority: 0.7, Description: "Environment example"},
		{Pattern: "schema.go", Priority: 0.8, Description: "Schema definitions"},
		{Pattern: "types.go", Priority: 0.8, Description: "Type definitions"},
		{Pattern: "*.proto", Priority: 0.85, Description: "Protocol buffer definitions"},
		{Pattern: "*.yaml", Priority: 0.7, Description: "YAML config files"},
		{Pattern: "*.json", Priority: 0.6, Description: "JSON files"},
	}
}

// GetPriority returns the priority for a file path based on matching patterns
// Returns 0.5 (neutral) if no patterns match
func (tsr *TrustedSourceRegistry) GetPriority(filePath string) float64 {
	if filePath == "" {
		return 0.5
	}

	// Get just the filename for matching
	fileName := filepath.Base(filePath)

	highestPriority := 0.5 // Default neutral priority

	for _, source := range tsr.sources {
		// Try matching against filename first
		matched, err := filepath.Match(source.Pattern, fileName)
		if err == nil && matched {
			if source.Priority > highestPriority {
				highestPriority = source.Priority
			}
			continue
		}

		// Try matching against full path for patterns with directories
		if strings.Contains(source.Pattern, "/") || strings.Contains(source.Pattern, "**") {
			// For ** patterns, do a simpler substring check
			if strings.Contains(source.Pattern, "**") {
				// Convert ** pattern to a simple check
				pattern := strings.ReplaceAll(source.Pattern, "**", "")
				pattern = strings.ReplaceAll(pattern, "*", "")
				if pattern != "" && strings.Contains(filePath, pattern) {
					if source.Priority > highestPriority {
						highestPriority = source.Priority
					}
				}
			} else {
				matched, err = filepath.Match(source.Pattern, filePath)
				if err == nil && matched {
					if source.Priority > highestPriority {
						highestPriority = source.Priority
					}
				}
			}
		}
	}

	return highestPriority
}

// IsTrusted returns true if a file path matches a trusted pattern with priority >= threshold
func (tsr *TrustedSourceRegistry) IsTrusted(filePath string, threshold float64) bool {
	return tsr.GetPriority(filePath) >= threshold
}

// GetMatchingPatterns returns all patterns that match a file path
func (tsr *TrustedSourceRegistry) GetMatchingPatterns(filePath string) []TrustedSource {
	if filePath == "" {
		return nil
	}

	fileName := filepath.Base(filePath)
	var matches []TrustedSource

	for _, source := range tsr.sources {
		matched, err := filepath.Match(source.Pattern, fileName)
		if err == nil && matched {
			matches = append(matches, source)
			continue
		}

		if strings.Contains(source.Pattern, "/") || strings.Contains(source.Pattern, "**") {
			if strings.Contains(source.Pattern, "**") {
				pattern := strings.ReplaceAll(source.Pattern, "**", "")
				pattern = strings.ReplaceAll(pattern, "*", "")
				if pattern != "" && strings.Contains(filePath, pattern) {
					matches = append(matches, source)
				}
			} else {
				matched, err = filepath.Match(source.Pattern, filePath)
				if err == nil && matched {
					matches = append(matches, source)
				}
			}
		}
	}

	return matches
}

// AddSource adds a new trusted source pattern
func (tsr *TrustedSourceRegistry) AddSource(source TrustedSource) {
	tsr.sources = append(tsr.sources, source)
}

// RemoveSource removes a trusted source pattern by pattern string
func (tsr *TrustedSourceRegistry) RemoveSource(pattern string) bool {
	for i, source := range tsr.sources {
		if source.Pattern == pattern {
			tsr.sources = append(tsr.sources[:i], tsr.sources[i+1:]...)
			return true
		}
	}
	return false
}

// ListSources returns all registered trusted sources
func (tsr *TrustedSourceRegistry) ListSources() []TrustedSource {
	result := make([]TrustedSource, len(tsr.sources))
	copy(result, tsr.sources)
	return result
}

// ApplyTrustBoost modifies search scores based on trusted source priorities
func (tsr *TrustedSourceRegistry) ApplyTrustBoost(entries []SearchResult) []SearchResult {
	for i := range entries {
		priority := tsr.GetPriority(entries[i].Entry.FilePath)
		// Boost score by priority factor (0.5 = neutral, >0.5 = boost, <0.5 = penalty)
		// Formula: score * (1 + (priority - 0.5) * 0.5)
		// This gives a range of 0.75x to 1.25x multiplier
		boost := 1 + (priority-0.5)*0.5
		entries[i].Score *= boost
	}
	return entries
}

// ApplyTrustBoostToContextEntries applies trust boost and sorts by score
func (tsr *TrustedSourceRegistry) ApplyTrustBoostToContextEntries(entries []ContextEntry) []ContextEntry {
	type scoredEntry struct {
		entry ContextEntry
		score float64
	}

	scored := make([]scoredEntry, len(entries))
	for i, entry := range entries {
		priority := tsr.GetPriority(entry.FilePath)
		scored[i] = scoredEntry{
			entry: entry,
			score: priority,
		}
	}

	// Sort by score descending (higher priority first)
	for i := 0; i < len(scored)-1; i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	result := make([]ContextEntry, len(entries))
	for i, se := range scored {
		result[i] = se.entry
	}
	return result
}
