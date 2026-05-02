// Package query provides Bazel query operations for finding affected tests.
package query

import (
	"context"
	"errors"
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

// errBazelCrash marks a query failure caused by a Bazel internal crash (e.g.,
// a JVM-level exception inside the Bazel server while fetching an external
// repository). Crashes are treated as non-fatal regardless of failOnError so
// a transient Bazel failure does not force callers to fall back to running
// the full test set.
var errBazelCrash = errors.New("bazel crashed")

// isBazelCrash reports whether the given stderr output indicates a Bazel
// internal crash as opposed to a regular query error (invalid syntax,
// missing target, etc.).
func isBazelCrash(stderr string) bool {
	return strings.Contains(stderr, "FATAL: bazel crashed")
}

// firstLine returns the first non-empty line of s, trimmed of whitespace.
// Bazel crash stack traces are long; a one-line summary keeps log output
// readable while the full stderr stays visible at the source.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// BazelQuerier executes Bazel queries.
type BazelQuerier struct {
	executor              executor.Executor
	failOnError           bool // If true, return errors from query failures; if false, log and continue
	enableSubpackageQuery bool // If true, run sub-package test queries (PKG/...)
}

// NewBazelQuerier creates a new BazelQuerier.
// By default, query failures are fatal (failOnError=true). Set the
// BAZEL_AFFECTED_TESTS_BEST_EFFORT environment variable to "true" or "1"
// to log warnings instead of returning errors.
func NewBazelQuerier() *BazelQuerier {
	bestEffort := os.Getenv("BAZEL_AFFECTED_TESTS_BEST_EFFORT") == "true" || os.Getenv("BAZEL_AFFECTED_TESTS_BEST_EFFORT") == "1"
	return &BazelQuerier{
		executor:              executor.NewBasicExecutor(),
		failOnError:           !bestEffort,
		enableSubpackageQuery: true,
	}
}

// NewBazelQuerierWithExecutor creates a new BazelQuerier with a custom executor.
// This is primarily useful for testing.
func NewBazelQuerierWithExecutor(exec executor.Executor) *BazelQuerier {
	bestEffort := os.Getenv("BAZEL_AFFECTED_TESTS_BEST_EFFORT") == "true" || os.Getenv("BAZEL_AFFECTED_TESTS_BEST_EFFORT") == "1"
	return &BazelQuerier{
		executor:              exec,
		failOnError:           !bestEffort,
		enableSubpackageQuery: true,
	}
}

// SetFailOnError controls whether Bazel query failures return errors (true)
// or are logged as warnings (false).
func (q *BazelQuerier) SetFailOnError(fail bool) {
	q.failOnError = fail
}

// SetEnableSubpackageQuery controls whether sub-package test queries (PKG/...)
// are executed. When disabled, only same-package and rdeps queries run.
func (q *BazelQuerier) SetEnableSubpackageQuery(enable bool) {
	q.enableSubpackageQuery = enable
}

// collectTests runs a Bazel query and adds the results to testsSet.
// Returns an error only when failOnError is true and the query fails with a
// non-crash error. Bazel internal crashes are always logged and skipped so
// callers do not escalate a transient Bazel failure into a full test fallback.
// Extra args are forwarded to the underlying bazel query invocation.
func (q *BazelQuerier) collectTests(queryStr, label, pkg string, testsSet map[string]bool, extraArgs ...string) error {
	tests, err := q.query(queryStr, extraArgs...)
	if err != nil {
		if errors.Is(err, errBazelCrash) {
			slog.Warn("Bazel crashed while querying "+label+", continuing with partial results", "package", pkg, "error", err)
			return nil
		}
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
		// Also skip when sub-package queries are disabled via config.
		switch {
		case !q.enableSubpackageQuery:
			slog.Debug("Skipping sub-package query (disabled by config)")
		case pkg == "//":
			slog.Debug("Skipping sub-package query for root package")
		default:
			if err := q.collectTests(
				fmt.Sprintf("kind('.*_test rule', %s/...)", pkg),
				"sub-package tests", pkg, testsSet,
			); err != nil {
				return nil, err
			}
		}

		// Get external test dependencies
		if err := q.collectTests(
			fmt.Sprintf("rdeps(//..., %s:*) intersect kind('.*_test rule', //...)", pkg),
			"external test deps", pkg, testsSet,
			"--keep_going", "--nohost_deps", "--noimplicit_deps",
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

// query executes a single bazel query and returns non-empty output lines.
// Extra args are inserted between the standard flags and the query string.
func (q *BazelQuerier) query(queryStr string, extraArgs ...string) ([]string, error) {
	raw, err := q.queryRaw(queryStr, extraArgs...)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}

	lines := strings.Split(strings.TrimSpace(raw), "\n")
	var results []string
	for _, line := range lines {
		if line != "" {
			results = append(results, line)
		}
	}
	return results, nil
}

// queryRaw runs bazel query and returns raw stdout. Empty results return "".
// Used for non-line-oriented outputs such as --output=xml.
func (q *BazelQuerier) queryRaw(queryStr string, extraArgs ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{"query"}
	args = append(args, extraArgs...)
	args = append(args, queryStr)

	result, err := q.executor.Execute(ctx, executor.ToolConfig{
		Command:        "bazel",
		Args:           args,
		Timeout:        30 * time.Second,
		CommandBuilder: &executor.ShellCommandBuilder{},
	})
	if err != nil {
		return "", fmt.Errorf("bazel query failed: %w", err)
	}

	// Check for lock contention - bazel exits with code 45 when another command is running
	if result.ExitCode == 45 || strings.Contains(result.Stderr, "Another command is running") {
		return "", fmt.Errorf("another bazel command is running; wait for it to complete or run 'bazel shutdown'")
	}

	// Bazel query may return non-zero exit code for empty results
	if result.ExitCode != 0 && result.Stderr == "" {
		return "", nil
	}

	if result.ExitCode != 0 {
		if isBazelCrash(result.Stderr) {
			return "", fmt.Errorf("bazel query crashed (exit code %d): %s: %w", result.ExitCode, firstLine(result.Stderr), errBazelCrash)
		}
		return "", fmt.Errorf("bazel query failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}

	return result.Output, nil
}
