package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runCommand(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("command failed: %s %v\nstdout:\n%s\nstderr:\n%s\nerror: %v", name, args, out.String(), errOut.String(), err)
	}
	return out.String()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root containing go.mod")
		}
		dir = parent
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile(%q) error: %v", path, err)
	}
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("ReadFile(%q) error: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

func TestCLI_CacheInvalidationAndNoCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("skipping: test uses shell scripts not supported on Windows")
	}

	root := repoRoot(t)
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "bazel-affected-tests")
	runCommand(t, root, nil, "go", "build", "-o", binPath, "./cmd/bazel-affected-tests")

	repoDir := t.TempDir()
	runCommand(t, repoDir, nil, "git", "init")

	writeFile(t, filepath.Join(repoDir, "pkg/foo/BUILD"), "# foo build\n", 0o600)
	writeFile(t, filepath.Join(repoDir, "pkg/foo/a.go"), "package foo\n", 0o600)
	writeFile(t, filepath.Join(repoDir, "WORKSPACE"), "# workspace\n", 0o600)

	runCommand(t, repoDir, nil, "git", "add", "pkg/foo/a.go")

	fakeBinDir := filepath.Join(repoDir, "fakebin")
	if err := os.MkdirAll(fakeBinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(fakebin) error: %v", err)
	}
	logPath := filepath.Join(repoDir, "bazel_calls.log")
	fakeBazelPath := filepath.Join(fakeBinDir, "bazel")
	fakeScript := `#!/bin/sh
set -eu
: "${BAZEL_CALL_LOG:?BAZEL_CALL_LOG must be set}"
query="${3:-}"
printf '%%s\n' "$query" >> "$BAZEL_CALL_LOG"
case "$query" in
  "kind('.*_test rule', //pkg/foo:*)")
    printf '%%s\n' "//pkg/foo:foo_test"
    ;;
  "rdeps(//..., //pkg/foo:*) intersect kind('.*_test rule', //...)")
    printf '%%s\n' "//dep:dep_test"
    ;;
  "kind('.*_test rule', //tools/format:*)"|"rdeps(//..., //tools/format:*) intersect kind('.*_test rule', //...)"|"//tools/format:* intersect kind('.*_test rule', //...)")
    ;;
  *)
    ;;
esac
`
	writeFile(t, fakeBazelPath, fakeScript, 0o755)

	cacheDir := filepath.Join(repoDir, "cache")
	// env is safe to reuse: os.Environ() returns a fresh slice each call.
	env := append(os.Environ(),
		"PATH="+fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BAZEL_CALL_LOG="+logPath,
	)

	runCLI := func(extraArgs ...string) {
		args := make([]string, 0, 2+len(extraArgs))
		args = append(args, "--cache-dir", cacheDir)
		args = append(args, extraArgs...)
		runCommand(t, repoDir, env, binPath, args...)
	}

	// Each full run queries: 3 for //pkg/foo + 3 for //tools/format = 6 queries.
	// A cache hit for //pkg/foo still runs 3 format queries = 3 queries.
	const fullRunQueries = 6
	const cacheHitQueries = 3 // only format queries

	// First run: full queries for //pkg/foo and //tools/format.
	runCLI()
	wantTotal := fullRunQueries
	if got := lineCount(t, logPath); got != wantTotal {
		t.Fatalf("first run bazel call count = %d, want %d", got, wantTotal)
	}

	// Second run: cache hit for //pkg/foo, only format queries.
	runCLI()
	wantTotal += cacheHitQueries
	if got := lineCount(t, logPath); got != wantTotal {
		t.Fatalf("second run (cache hit) call count = %d, want %d", got, wantTotal)
	}

	// Unstaged BUILD change invalidates cache; full queries again.
	writeFile(t, filepath.Join(repoDir, "pkg/foo/BUILD"), "# foo build changed\n", 0o600)
	runCLI()
	wantTotal += fullRunQueries
	if got := lineCount(t, logPath); got != wantTotal {
		t.Fatalf("after unstaged BUILD change, call count = %d, want %d", got, wantTotal)
	}

	// WORKSPACE change should NOT invalidate cache; only format queries.
	writeFile(t, filepath.Join(repoDir, "WORKSPACE"), "# workspace changed\n", 0o600)
	runCLI()
	wantTotal += cacheHitQueries
	if got := lineCount(t, logPath); got != wantTotal {
		t.Fatalf("after WORKSPACE change (cache hit), call count = %d, want %d", got, wantTotal)
	}

	// --no-cache forces full queries regardless of cache state.
	runCLI("--no-cache")
	wantTotal += fullRunQueries
	if got := lineCount(t, logPath); got != wantTotal {
		t.Fatalf("--no-cache should force queries, call count = %d, want %d", got, wantTotal)
	}
}
