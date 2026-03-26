// Package config provides configuration file loading and pattern matching
// for bazel-affected-tests.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const ConfigFileName = ".bazel-affected-tests.yaml"

// Config represents the configuration file structure.
type Config struct {
	// Version is the configuration file format version. Currently only 1 is supported.
	Version int `yaml:"version"`
	// IgnorePaths is a list of glob patterns for file paths to skip before
	// package resolution. Files matching these patterns are excluded from all
	// processing — no package lookup and no test discovery.
	IgnorePaths []string `yaml:"ignore_paths"`
	// Exclude is a list of path.Match patterns for targets to exclude from query results.
	Exclude []string `yaml:"exclude"`
	// Rules maps file glob patterns to Bazel targets to include when matched.
	Rules []Rule `yaml:"rules"`
}

// Rule maps glob patterns to Bazel targets. When any staged file matches one of
// the Patterns, all corresponding Targets are included in the output.
type Rule struct {
	// Patterns is a list of glob patterns to match against staged file paths.
	Patterns []string `yaml:"patterns"`
	// Targets is a list of Bazel target labels to include when a pattern matches.
	Targets []string `yaml:"targets"`
}

// LoadConfig loads the configuration from .bazel-affected-tests.yaml in the given directory.
// Returns nil, nil if the file does not exist.
// Returns nil, error if the file exists but cannot be parsed.
func LoadConfig(configDir string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(configDir, ConfigFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// FilterIgnoredFiles returns files that do not match any ignore_paths pattern.
// Patterns use the same glob syntax as rule patterns (e.g., ".semgrep/**", "docs/**", "*.md").
func (c *Config) FilterIgnoredFiles(files []string) []string {
	if len(c.IgnorePaths) == 0 {
		return files
	}
	var filtered []string
	for _, file := range files {
		if !c.shouldIgnoreFile(file) {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

// shouldIgnoreFile reports whether the given file matches any ignore_paths pattern.
func (c *Config) shouldIgnoreFile(file string) bool {
	for _, pattern := range c.IgnorePaths {
		if MatchPattern(pattern, file) {
			return true
		}
	}
	return false
}

// ShouldExclude reports whether the given target matches any exclude pattern.
// Patterns use path.Match syntax (e.g., "//tools/format:*").
func (c *Config) ShouldExclude(target string) bool {
	for _, pattern := range c.Exclude {
		if matched, _ := path.Match(pattern, target); matched {
			return true
		}
	}
	return false
}

// FilterExcluded returns tests with excluded targets removed.
func (c *Config) FilterExcluded(tests []string) []string {
	if len(c.Exclude) == 0 {
		return tests
	}
	var filtered []string
	for _, test := range tests {
		if !c.ShouldExclude(test) {
			filtered = append(filtered, test)
		}
	}
	return filtered
}

// MatchTargets returns all targets whose patterns match any of the given files.
func (c *Config) MatchTargets(files []string) []string {
	targetSet := make(map[string]bool)

	for _, rule := range c.Rules {
		matched := false
		for _, pattern := range rule.Patterns {
			for _, file := range files {
				if MatchPattern(pattern, file) {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}

		if matched {
			for _, target := range rule.Targets {
				targetSet[target] = true
			}
		}
	}

	// Convert set to slice
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}

	return targets
}
