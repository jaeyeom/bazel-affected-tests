// Package query provides Bazel query operations for finding affected tests.
package query

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	executor "github.com/jaeyeom/go-cmdexec"
)

// validPkgPattern validates Bazel package labels.
var validPkgPattern = regexp.MustCompile(`^//[a-zA-Z0-9_./-]*$`)

// BazelQuerier executes Bazel queries.
type BazelQuerier struct {
	executor    executor.Executor
	failOnError bool // If true, return errors from query failures; if false, log and continue
}

// NewBazelQuerier creates a new BazelQuerier.
func NewBazelQuerier() *BazelQuerier {
	failOnError := os.Getenv("BAZEL_AFFECTED_TESTS_FAIL_ON_ERROR") == "true" || os.Getenv("BAZEL_AFFECTED_TESTS_FAIL_ON_ERROR") == "1"
	return &BazelQuerier{
		executor:    executor.NewBasicExecutor(),
		failOnError: failOnError,
	}
}

// NewBazelQuerierWithExecutor creates a new BazelQuerier with a custom executor.
// This is primarily useful for testing.
func NewBazelQuerierWithExecutor(exec executor.Executor) *BazelQuerier {
	failOnError := os.Getenv("BAZEL_AFFECTED_TESTS_FAIL_ON_ERROR") == "true" || os.Getenv("BAZEL_AFFECTED_TESTS_FAIL_ON_ERROR") == "1"
	return &BazelQuerier{
		executor:    exec,
		failOnError: failOnError,
	}
}

// collectTests runs a Bazel query and adds the results to testsSet.
// Returns an error only when failOnError is true and the query fails.
func (q *BazelQuerier) collectTests(queryStr, label, pkg string, testsSet map[string]bool) error {
	tests, err := q.query(queryStr)
	if err != nil {
		if q.failOnError {
			return fmt.Errorf("failed to query %s for %s: %w", label, pkg, err)
		}
		slog.Warn("Error querying "+label+", continuing...", "package", pkg, "error", err)
		return nil
	}
	slog.Debug(label+" found", "count", len(tests))
	for _, test := range tests {
		testsSet[test] = true
	}
	return nil
}

// FindAffectedTests finds test targets affected by changes to the given packages.
func (q *BazelQuerier) FindAffectedTests(packages []string) ([]string, error) {
	if len(packages) == 0 {
		return nil, nil
	}

	// Deduplicate packages
	uniquePackages := make(map[string]bool)
	for _, pkg := range packages {
		uniquePackages[pkg] = true
	}

	var allTests []string
	testsSet := make(map[string]bool)

	// Process each unique package
	for pkg := range uniquePackages {
		if !validPkgPattern.MatchString(pkg) {
			slog.Warn("Skipping invalid package label", "package", pkg)
			continue
		}

		slog.Debug("Processing package", "package", pkg)

		// Get tests in the same package
		if err := q.collectTests(
			fmt.Sprintf("kind('.*_test rule', %s:*)", pkg),
			"same package tests", pkg, testsSet,
		); err != nil {
			return nil, err
		}

		// Get tests in sub-packages (e.g., golden tests in child directories).
		// Skip for root package "//" because "///..." resolves to "//..." which
		// matches every test in the entire workspace.
		if pkg != "//" {
			if err := q.collectTests(
				fmt.Sprintf("kind('.*_test rule', %s/...)", pkg),
				"sub-package tests", pkg, testsSet,
			); err != nil {
				return nil, err
			}
		} else {
			slog.Debug("Skipping sub-package query for root package")
		}

		// Get external test dependencies
		if err := q.collectTests(
			fmt.Sprintf("rdeps(//..., %s:*) intersect kind('.*_test rule', //...)", pkg),
			"external test deps", pkg, testsSet,
		); err != nil {
			return nil, err
		}
	}

	// Convert set to slice
	for test := range testsSet {
		allTests = append(allTests, test)
	}

	return allTests, nil
}

// query executes a single bazel query.
func (q *BazelQuerier) query(queryStr string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := q.executor.Execute(ctx, executor.ToolConfig{
		Command:        "bazel",
		Args:           []string{"query", "--noblock_for_lock", queryStr},
		Timeout:        30 * time.Second,
		CommandBuilder: &executor.ShellCommandBuilder{},
	})
	if err != nil {
		return nil, fmt.Errorf("bazel query failed: %w", err)
	}

	// Check for lock contention - bazel exits with code 45 when another command is running
	if result.ExitCode == 45 || strings.Contains(result.Stderr, "Another command is running") {
		return nil, fmt.Errorf("another bazel command is running; wait for it to complete or run 'bazel shutdown'")
	}

	// Bazel query may return non-zero exit code for empty results
	if result.ExitCode != 0 && result.Stderr == "" {
		return nil, nil
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("bazel query failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}

	if len(result.Output) == 0 {
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(result.Output), "\n")
	var results []string
	for _, line := range lines {
		if line != "" {
			results = append(results, line)
		}
	}
	return results, nil
}
