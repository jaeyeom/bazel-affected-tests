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

// GetDiffFiles returns files that differ from the given ref.
func GetDiffFiles(ctx context.Context, exec executor.Executor, ref string) ([]string, error) {
	return getDiffFiles(ctx, exec, "git", "diff", ref, "--name-only", "--diff-filter=ACM")
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
