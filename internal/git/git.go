// Package git provides Git operations for bazel-affected-tests.
package git

import (
	"context"
	"fmt"
	"strings"

	executor "github.com/jaeyeom/go-cmdexec"
)

// RepoRoot returns the absolute path to the top-level directory of the git
// repository. It shells out to "git rev-parse --show-toplevel".
func RepoRoot(ctx context.Context, exec executor.Executor) (string, error) {
	output, err := executor.Output(ctx, exec, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("failed to find git repo root: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetStagedFiles returns the list of staged files (Added, Copied, Modified - not Deleted).
func GetStagedFiles(ctx context.Context, exec executor.Executor) ([]string, error) {
	return getDiffFiles(ctx, exec, "git", "diff", "--cached", "--name-only", "--diff-filter=ACM")
}

// GetHeadFiles returns files that differ from HEAD (staged + unstaged changes).
func GetHeadFiles(ctx context.Context, exec executor.Executor) ([]string, error) {
	return getDiffFiles(ctx, exec, "git", "diff", "HEAD", "--name-only", "--diff-filter=ACM")
}

// GetDiffFiles returns files that differ from the given ref (working tree vs ref).
func GetDiffFiles(ctx context.Context, exec executor.Executor, ref string) ([]string, error) {
	return getDiffFiles(ctx, exec, "git", "diff", ref, "--name-only", "--diff-filter=ACM")
}

// GetDiffBetween returns files that differ between two tree-ishes (no working tree).
// Equivalent to: git diff --name-only --diff-filter=ACM <old> <new>.
func GetDiffBetween(ctx context.Context, exec executor.Executor, oldRev, newRev string) ([]string, error) {
	return getDiffFiles(ctx, exec, "git", "diff", oldRev, newRev, "--name-only", "--diff-filter=ACM")
}

// GetDiffRange returns files for a git range expression such as "A..B" or "A...B".
// Three-dot form is PR-scope (merge-base of A and B through B).
func GetDiffRange(ctx context.Context, exec executor.Executor, rangeSpec string) ([]string, error) {
	return getDiffFiles(ctx, exec, "git", "diff", rangeSpec, "--name-only", "--diff-filter=ACM")
}

// MergeBase returns the best common ancestor of a and b.
func MergeBase(ctx context.Context, exec executor.Executor, a, b string) (string, error) {
	output, err := executor.Output(ctx, exec, "git", "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("failed to find merge-base of %q and %q: %w", a, b, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// RefExists reports whether ref resolves in the repository.
func RefExists(ctx context.Context, exec executor.Executor, ref string) bool {
	_, err := executor.Output(ctx, exec, "git", "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// defaultBaseCandidates are tried in order when resolving the integration branch
// for PR-scope push diffs.
var defaultBaseCandidates = []string{
	"origin/main",
	"main",
	"origin/master",
	"master",
}

// ResolveDefaultBase returns the first existing ref among preferred (if non-empty)
// and the built-in candidates (origin/main, main, origin/master, master).
func ResolveDefaultBase(ctx context.Context, exec executor.Executor, preferred string) (string, error) {
	var candidates []string
	if preferred != "" {
		candidates = append(candidates, preferred)
	}
	candidates = append(candidates, defaultBaseCandidates...)
	for _, c := range candidates {
		if RefExists(ctx, exec, c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("could not resolve default base branch (tried %v)", candidates)
}

func getDiffFiles(ctx context.Context, exec executor.Executor, name string, args ...string) ([]string, error) {
	output, err := executor.Output(ctx, exec, name, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get diff files: %w", err)
	}

	if len(output) == 0 {
		return []string{}, nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var files []string
	for _, line := range lines {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
