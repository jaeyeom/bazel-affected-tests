// Real-git integration suite for --pre-push and --diff (issue #21).
//
// These tests drive the built CLI against temporary git repositories and a
// fake bazel on PATH. They do not require a real Bazel installation and always
// run in CI (only skipped on Windows, where the fake bazel shell script is
// not supported).
//
// Run locally:
//
//	go test ./cmd/bazel-affected-tests -run PrePush -count=1
//	# or the full suite:
//	make test
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// zeroOID is the all-zero object name used by git hooks for new/deleted refs.
const zeroOID = "0000000000000000000000000000000000000000"

var (
	prePushBinOnce sync.Once
	prePushBinPath string
	prePushBinErr  error
)

// prePushBinary builds the CLI once per process for this suite.
func prePushBinary(t *testing.T) string {
	t.Helper()
	prePushBinOnce.Do(func() {
		root := repoRoot(t)
		dir, err := os.MkdirTemp("", "bazel-affected-tests-bin-*")
		if err != nil {
			prePushBinErr = err
			return
		}
		prePushBinPath = filepath.Join(dir, "bazel-affected-tests")
		cmd := exec.Command("go", "build", "-o", prePushBinPath, "./cmd/bazel-affected-tests")
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			prePushBinErr = fmt.Errorf("go build: %w\n%s", err, stderr.String())
		}
	})
	if prePushBinErr != nil {
		t.Fatalf("building test binary: %v", prePushBinErr)
	}
	return prePushBinPath
}

func skipPrePushWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping: fake bazel shell script not supported on Windows")
	}
}

// prePushRepo is a temporary git workspace with a fake bazel for package→target mapping.
type prePushRepo struct {
	t    *testing.T
	dir  string
	bin  string
	env  []string
	fake string // fakebin directory
}

func newPrePushRepo(t *testing.T) *prePushRepo {
	t.Helper()
	skipPrePushWindows(t)

	dir := t.TempDir()
	runCommand(t, dir, nil, "git", "init", "-b", "main")
	runCommand(t, dir, nil, "git", "config", "user.email", "test@test.com")
	runCommand(t, dir, nil, "git", "config", "user.name", "Test")
	runCommand(t, dir, nil, "git", "config", "commit.gpgsign", "false")

	// Minimal workspace markers (package discovery uses BUILD files only).
	writeFile(t, filepath.Join(dir, "WORKSPACE"), "# test workspace\n", 0o644)
	writeFile(t, filepath.Join(dir, "BUILD.bazel"), "", 0o644)

	fakeBin := filepath.Join(dir, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("MkdirAll(fakebin): %v", err)
	}
	// Last argv is the query expression (may be preceded by --keep_going etc.).
	// Emit //pkg/<name>:<name>_test for any //pkg/<name> mentioned in the query.
	fakeScript := `#!/bin/sh
set -eu
query=""
for a in "$@"; do query="$a"; done
pkg=$(printf '%s\n' "$query" | sed -n 's/.*\(\/\/pkg\/[a-zA-Z0-9_-]*\).*/\1/p' | head -1)
if [ -n "$pkg" ]; then
  name=${pkg##*/}
  printf '%s\n' "${pkg}:${name}_test"
fi
`
	writeFile(t, filepath.Join(fakeBin, "bazel"), fakeScript, 0o755)

	env := append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Ensure strict query mode regardless of developer environment.
		"BAZEL_AFFECTED_TESTS_BEST_EFFORT=",
	)

	return &prePushRepo{
		t:    t,
		dir:  dir,
		bin:  prePushBinary(t),
		env:  env,
		fake: fakeBin,
	}
}

func (r *prePushRepo) git(args ...string) string {
	r.t.Helper()
	return strings.TrimSpace(runCommand(r.t, r.dir, nil, "git", args...))
}

func (r *prePushRepo) revParse(rev string) string {
	r.t.Helper()
	return r.git("rev-parse", rev)
}

// writePkg creates pkg/<name>/BUILD.bazel and pkg/<name>/<name>.go.
func (r *prePushRepo) writePkg(name, content string) {
	r.t.Helper()
	pkgDir := filepath.Join(r.dir, "pkg", name)
	writeFile(r.t, filepath.Join(pkgDir, "BUILD.bazel"), "# package\n", 0o644)
	writeFile(r.t, filepath.Join(pkgDir, name+".go"), content, 0o644)
}

func (r *prePushRepo) commitAll(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-m", msg)
	return r.revParse("HEAD")
}

// runCLI invokes the binary in the repo. stdin may be empty.
func (r *prePushRepo) runCLI(stdin string, args ...string) (stdout, stderr string, code int) {
	r.t.Helper()
	return runCLIBinary(r.t, r.dir, r.env, r.bin, stdin, args...)
}

// runCLIBinary runs the built CLI and returns stdout, stderr, and exit code.
// The binary path is always produced by prePushBinary in this test suite.
func runCLIBinary(t *testing.T, dir string, env []string, bin, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	//nolint:gosec // G204: bin is the suite-built test binary; args are test literals.
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("command failed unexpectedly: %v\nstderr:\n%s", err, errBuf.String())
		}
		return outBuf.String(), errBuf.String(), ee.ExitCode()
	}
	return outBuf.String(), errBuf.String(), 0
}

func (r *prePushRepo) mustTargets(stdin string, args ...string) []string {
	r.t.Helper()
	stdout, stderr, code := r.runCLI(stdin, args...)
	if code != 0 {
		r.t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s\nargs: %v", code, stdout, stderr, args)
	}
	return parseTargetLines(stdout)
}

func (r *prePushRepo) mustEmpty(stdin string, args ...string) {
	r.t.Helper()
	got := r.mustTargets(stdin, args...)
	if len(got) != 0 {
		r.t.Fatalf("got targets %v, want empty", got)
	}
}

func parseTargetLines(stdout string) []string {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	sort.Strings(lines)
	return lines
}

func assertTargets(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets = %v, want %v", got, want)
		}
	}
}

func pushLine(localRef, localOID, remoteRef, remoteOID string) string {
	return fmt.Sprintf("%s %s %s %s\n", localRef, localOID, remoteRef, remoteOID)
}

// seedMain creates main with pkg/a and returns the commit oid.
func (r *prePushRepo) seedMain() string {
	r.t.Helper()
	r.writePkg("a", "package a\n// v1\n")
	return r.commitAll("initial main")
}

// --- Scenarios A–L ---

func TestPrePush_A_CleanTreeIncrementalPush(t *testing.T) {
	r := newPrePushRepo(t)
	mainOID := r.seedMain()

	r.git("checkout", "-b", "feature")
	r.writePkg("b", "package b\n// feature\n")
	featureOID := r.commitAll("add pkg/b")

	// Clean tree: nothing staged or unstaged.
	status := r.git("status", "--porcelain")
	if status != "" {
		t.Fatalf("want clean tree, got status %q", status)
	}

	stdin := pushLine("refs/heads/feature", featureOID, "refs/heads/feature", mainOID)
	got := r.mustTargets(stdin, "--pre-push", "--no-cache")
	assertTargets(t, got, []string{"//pkg/b:b_test"})
}

func TestPrePush_B_NewBranchZeroRemoteOID(t *testing.T) {
	r := newPrePushRepo(t)
	_ = r.seedMain()

	r.git("checkout", "-b", "feature")
	r.writePkg("b", "package b\n// new branch\n")
	featureOID := r.commitAll("add pkg/b on new branch")

	stdin := pushLine("refs/heads/feature", featureOID, "refs/heads/feature", zeroOID)
	got := r.mustTargets(stdin, "--pre-push", "--no-cache", "--pre-push-base", "main")
	assertTargets(t, got, []string{"//pkg/b:b_test"})
}

func TestPrePush_C_RebaseRewritePRScope(t *testing.T) {
	r := newPrePushRepo(t)
	_ = r.seedMain()

	// Feature adds pkg/b off initial main.
	r.git("checkout", "-b", "feature")
	r.writePkg("b", "package b\n// branch work\n")
	oldFeatureOID := r.commitAll("feature: add b")

	// Main advances with unrelated pkg/c (simulates integration branch moving).
	r.git("checkout", "main")
	r.writePkg("c", "package c\n// main advance\n")
	_ = r.commitAll("main: add c")

	// Rebase feature onto advanced main. Content of b is unchanged; tip tree
	// differs from old tip mainly by pkg/c (from main), not by pkg/b.
	r.git("checkout", "feature")
	r.git("rebase", "main")
	newFeatureOID := r.revParse("HEAD")

	// Tip-to-tip under-selects: old tip already has b, so b is missing.
	tipStdin := pushLine("refs/heads/feature", newFeatureOID, "refs/heads/feature", oldFeatureOID)
	tipGot := r.mustTargets(tipStdin, "--pre-push", "--pre-push-tip", "--no-cache")
	for _, tgt := range tipGot {
		if tgt == "//pkg/b:b_test" {
			t.Fatalf("tip-to-tip unexpectedly selected branch file target %v (got %v)", tgt, tipGot)
		}
	}

	// PR-scope vs main includes full branch files (pkg/b).
	prStdin := pushLine("refs/heads/feature", newFeatureOID, "refs/heads/feature", oldFeatureOID)
	prGot := r.mustTargets(prStdin, "--pre-push", "--no-cache", "--pre-push-base", "main")
	assertTargets(t, prGot, []string{"//pkg/b:b_test"})
}

func TestPrePush_D_PrePushTipVsDefaultPR(t *testing.T) {
	// Same history as C: tip-to-tip and PR-scope must differ.
	r := newPrePushRepo(t)
	_ = r.seedMain()

	r.git("checkout", "-b", "feature")
	r.writePkg("b", "package b\n// branch\n")
	oldFeatureOID := r.commitAll("feature b")

	r.git("checkout", "main")
	r.writePkg("c", "package c\n// main\n")
	_ = r.commitAll("main c")

	r.git("checkout", "feature")
	r.git("rebase", "main")
	newFeatureOID := r.revParse("HEAD")

	stdin := pushLine("refs/heads/feature", newFeatureOID, "refs/heads/feature", oldFeatureOID)

	prGot := r.mustTargets(stdin, "--pre-push", "--no-cache", "--pre-push-base", "main")
	tipGot := r.mustTargets(stdin, "--pre-push", "--pre-push-tip", "--no-cache")

	if len(prGot) == 0 {
		t.Fatal("PR-scope returned empty targets")
	}
	// Sets must differ when tip-to-tip under-selects the branch file.
	if equalStringSets(prGot, tipGot) {
		t.Fatalf("expected PR-scope %v != tip-to-tip %v", prGot, tipGot)
	}
	// PR must include branch package; tip must not (see scenario C).
	if !contains(prGot, "//pkg/b:b_test") {
		t.Fatalf("PR-scope missing //pkg/b:b_test: %v", prGot)
	}
	if contains(tipGot, "//pkg/b:b_test") {
		t.Fatalf("tip-to-tip should under-select //pkg/b:b_test: %v", tipGot)
	}
}

func TestPrePush_E_DiffTwoDotAndThreeDot(t *testing.T) {
	r := newPrePushRepo(t)
	baseOID := r.seedMain()

	r.git("checkout", "-b", "feature")
	r.writePkg("b", "package b\n// feature\n")
	featureOID := r.commitAll("add b")

	// Advance main after the branch point so history is non-trivial; three-dot
	// merge-base with feature remains baseOID (feature not rebased).
	r.git("checkout", "main")
	r.writePkg("c", "package c\n// on main\n")
	_ = r.commitAll("add c")
	r.git("checkout", "feature")

	// Two-dot baseOID..featureOID: files introduced on feature from base.
	// Three-dot main...feature: merge-base(main,feature)..feature = still b only.
	gitTwoDot := filesFromGitDiff(t, r.dir, baseOID, featureOID, false)
	gitThreeDot := filesFromGitDiff(t, r.dir, "main", featureOID, true)

	twoDotOut := r.mustTargets("", "--diff", baseOID+".."+featureOID, "--no-cache")
	threeDotOut := r.mustTargets("", "--diff", "main..."+featureOID, "--no-cache")

	// Map git file lists through the same package→target expectation.
	assertTargets(t, twoDotOut, targetsForFiles(gitTwoDot))
	assertTargets(t, threeDotOut, targetsForFiles(gitThreeDot))

	// Sanity: feature only added pkg/b.
	assertTargets(t, twoDotOut, []string{"//pkg/b:b_test"})
	assertTargets(t, threeDotOut, []string{"//pkg/b:b_test"})
}

func TestPrePush_F_BranchDelete(t *testing.T) {
	r := newPrePushRepo(t)
	mainOID := r.seedMain()

	// local_oid zero → branch delete; nothing to test.
	stdin := pushLine("refs/heads/feature", zeroOID, "refs/heads/feature", mainOID)
	r.mustEmpty(stdin, "--pre-push", "--no-cache")
}

func TestPrePush_G_TagOnlyPush(t *testing.T) {
	r := newPrePushRepo(t)
	mainOID := r.seedMain()

	stdin := pushLine("refs/tags/v1.0.0", mainOID, "refs/tags/v1.0.0", zeroOID)
	r.mustEmpty(stdin, "--pre-push", "--no-cache")
}

func TestPrePush_H_MultiRefPushUnion(t *testing.T) {
	r := newPrePushRepo(t)
	mainOID := r.seedMain()

	r.git("checkout", "-b", "feat-a")
	// Modify existing package a (not a new package) so each branch has a distinct package.
	writeFile(t, filepath.Join(r.dir, "pkg", "a", "a.go"), "package a\n// feat-a\n", 0o644)
	featAOID := r.commitAll("feat-a touch a")

	r.git("checkout", "main")
	r.git("checkout", "-b", "feat-b")
	r.writePkg("b", "package b\n// feat-b\n")
	featBOID := r.commitAll("feat-b add b")

	stdin := pushLine("refs/heads/feat-a", featAOID, "refs/heads/feat-a", mainOID) +
		pushLine("refs/heads/feat-b", featBOID, "refs/heads/feat-b", mainOID)

	got := r.mustTargets(stdin, "--pre-push", "--no-cache", "--pre-push-base", "main")
	assertTargets(t, got, []string{"//pkg/a:a_test", "//pkg/b:b_test"})
}

func TestPrePush_I_MalformedStdin(t *testing.T) {
	r := newPrePushRepo(t)
	_ = r.seedMain()

	stdout, stderr, code := r.runCLI("not-enough-fields\n", "--pre-push", "--no-cache")
	if code == 0 {
		t.Fatalf("expected nonzero exit for malformed stdin; stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "pre-push") && !strings.Contains(stderr, "want 4 fields") && !strings.Contains(stderr, "Error") {
		t.Fatalf("stderr should explain the error, got: %q", stderr)
	}
}

func TestPrePush_J_NoDefaultBase(t *testing.T) {
	skipPrePushWindows(t)

	dir := t.TempDir()
	// Only branch "feature" — no main/master/origin/* candidates.
	runCommand(t, dir, nil, "git", "init", "-b", "feature")
	runCommand(t, dir, nil, "git", "config", "user.email", "test@test.com")
	runCommand(t, dir, nil, "git", "config", "user.name", "Test")
	runCommand(t, dir, nil, "git", "config", "commit.gpgsign", "false")

	writeFile(t, filepath.Join(dir, "WORKSPACE"), "# ws\n", 0o644)
	writeFile(t, filepath.Join(dir, "BUILD.bazel"), "", 0o644)
	writeFile(t, filepath.Join(dir, "pkg", "a", "BUILD.bazel"), "#\n", 0o644)
	writeFile(t, filepath.Join(dir, "pkg", "a", "a.go"), "package a\n", 0o644)
	runCommand(t, dir, nil, "git", "add", "-A")
	runCommand(t, dir, nil, "git", "commit", "-m", "orphan feature")

	localOID := strings.TrimSpace(runCommand(t, dir, nil, "git", "rev-parse", "HEAD"))
	stdin := pushLine("refs/heads/feature", localOID, "refs/heads/feature", zeroOID)

	bin := prePushBinary(t)
	stdout, stderr, code := runCLIBinary(t, dir, nil, bin, stdin, "--pre-push", "--no-cache")
	if code == 0 {
		t.Fatalf("expected error when no default base exists; stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "default base") && !strings.Contains(stderr, "base branch") {
		t.Fatalf("stderr should mention missing default base, got: %q", stderr)
	}
}

func TestPrePush_K_MutualExclusion(t *testing.T) {
	r := newPrePushRepo(t)
	_ = r.seedMain()

	stdout, stderr, code := r.runCLI("", "--pre-push", "--staged", "--no-cache")
	if code == 0 {
		t.Fatalf("expected failure for --pre-push + --staged; stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("stderr should mention mutual exclusion, got: %q", stderr)
	}
}

func TestPrePush_L_WorkingTreeFootgunRegression(t *testing.T) {
	r := newPrePushRepo(t)
	mainOID := r.seedMain()

	r.git("checkout", "-b", "feature")
	r.writePkg("b", "package b\n// footgun\n")
	featureOID := r.commitAll("add b")

	// Clean tree + no flags → auto mode sees no staged/HEAD diffs → empty.
	status := r.git("status", "--porcelain")
	if status != "" {
		t.Fatalf("want clean tree, got %q", status)
	}
	r.mustEmpty("", "--no-cache")

	// Same commits with --pre-push → non-empty.
	stdin := pushLine("refs/heads/feature", featureOID, "refs/heads/feature", mainOID)
	got := r.mustTargets(stdin, "--pre-push", "--no-cache")
	assertTargets(t, got, []string{"//pkg/b:b_test"})
}

// --- helpers unique to this file ---

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// filesFromGitDiff returns paths from real git, matching the tool's filters.
func filesFromGitDiff(t *testing.T, dir, oldRev, newRev string, threeDot bool) []string {
	t.Helper()
	var out string
	if threeDot {
		out = runCommand(t, dir, nil, "git", "diff", oldRev+"..."+newRev, "--name-only", "--diff-filter=ACM")
	} else {
		out = runCommand(t, dir, nil, "git", "diff", oldRev, newRev, "--name-only", "--diff-filter=ACM")
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// targetsForFiles maps repo-relative source paths under pkg/<name>/ to fake targets.
func targetsForFiles(files []string) []string {
	seen := make(map[string]bool)
	var targets []string
	for _, f := range files {
		// Expect pkg/<name>/...
		parts := strings.Split(filepath.ToSlash(f), "/")
		if len(parts) < 2 || parts[0] != "pkg" {
			continue
		}
		name := parts[1]
		tgt := fmt.Sprintf("//pkg/%s:%s_test", name, name)
		if !seen[tgt] {
			seen[tgt] = true
			targets = append(targets, tgt)
		}
	}
	sort.Strings(targets)
	return targets
}
