// Contract and smoke tests for examples/pre-push (issue #22).
//
// The example hook is the documented install path. These tests treat it as a
// contract so CI catches drift (wrong flags, best-effort, remote-arg forwarding).
//
// Smoke tests use a PATH stub for bazel-affected-tests — no network, no real Bazel.
package examples_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func examplePrePushPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "pre-push")
}

// stripShellComments drops full-line and trailing # comments so flag checks
// ignore documentation that mentions --best-effort as something to avoid.
func stripShellComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestPrePushExample_Contract(t *testing.T) {
	path := examplePrePushPath(t)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat examples/pre-push: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("examples/pre-push is not executable (mode %v); install path requires +x", info.Mode())
	}

	// Git index mode is the durable contract (mode bit survives checkout).
	// Format of `git ls-files -s`: <mode> <object> <stage>\t<path>
	repoRoot := filepath.Dir(filepath.Dir(path))
	cmd := exec.Command("git", "ls-files", "-s", "--", "examples/pre-push")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files -s examples/pre-push: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 1 {
		t.Fatalf("git ls-files -s returned empty output; is examples/pre-push tracked?")
	}
	if fields[0] != "100755" {
		t.Errorf("git mode = %q, want 100755 (executable in index)", fields[0])
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read examples/pre-push: %v", err)
	}
	body := stripShellComments(string(raw))

	for _, flag := range []string{"--pre-push", "--run", "--fail-fast"} {
		if !strings.Contains(body, flag) {
			t.Errorf("examples/pre-push body must contain %q", flag)
		}
	}
	if strings.Contains(body, "--best-effort") {
		t.Error("examples/pre-push must not pass --best-effort (blocking push gate must fail closed)")
	}
	// Remote name/URL must not become tool args.
	if strings.Contains(body, `"$@"`) || strings.Contains(body, "$@") {
		t.Error(`examples/pre-push must not forward "$@" / $@ to the tool`)
	}
	if !strings.Contains(body, "set -euo pipefail") && !strings.Contains(body, "set -eu") {
		t.Error("examples/pre-push must use set -euo pipefail (or equivalent fail-closed shell options)")
	}
}

func TestPrePushExample_Smoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping: PATH stub shell script not supported on Windows")
	}

	path := examplePrePushPath(t)
	dir := t.TempDir()
	stubBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(stubBin, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	argvFile := filepath.Join(dir, "argv")
	stdinFile := filepath.Join(dir, "stdin")
	exitFile := filepath.Join(dir, "exitcode")

	// Stub records argv + stdin and exits with the code written to STUB_EXIT_FILE (default 0).
	stub := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$STUB_ARGV_FILE"
cat > "$STUB_STDIN_FILE"
code=0
if [ -f "$STUB_EXIT_FILE" ]; then
  code=$(cat "$STUB_EXIT_FILE")
fi
exit "$code"
`
	stubPath := filepath.Join(stubBin, "bazel-affected-tests")
	//nolint:gosec // G306: PATH stub must be executable
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	sampleStdin := "refs/heads/feature abc111 refs/heads/feature def222\n"
	env := append(os.Environ(),
		"PATH="+stubBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_ARGV_FILE="+argvFile,
		"STUB_STDIN_FILE="+stdinFile,
		"STUB_EXIT_FILE="+exitFile,
	)

	t.Run("forwards pre-push protocol and required flags", func(t *testing.T) {
		_ = os.Remove(argvFile)
		_ = os.Remove(stdinFile)
		if err := os.WriteFile(exitFile, []byte("0\n"), 0o600); err != nil {
			t.Fatalf("write exitcode: %v", err)
		}

		// Git pre-push passes remote name and URL as $1 $2; they must not reach the tool.
		//nolint:gosec // G204: path is examples/pre-push from the repo; args are test literals.
		cmd := exec.Command(path, "origin", "git@github.com:example/repo.git")
		cmd.Env = env
		cmd.Stdin = strings.NewReader(sampleStdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("examples/pre-push exit error: %v\nstderr:\n%s", err, stderr.String())
		}

		argvBytes, err := os.ReadFile(argvFile)
		if err != nil {
			t.Fatalf("read stub argv: %v", err)
		}
		argv := strings.Split(strings.TrimSpace(string(argvBytes)), "\n")
		wantFlags := map[string]bool{"--pre-push": false, "--run": false, "--fail-fast": false}
		for _, a := range argv {
			if _, ok := wantFlags[a]; ok {
				wantFlags[a] = true
			}
			if a == "origin" || strings.Contains(a, "github.com") {
				t.Errorf("stub received remote arg %q; hook must not forward $1/$2/\"$@\"", a)
			}
		}
		for flag, seen := range wantFlags {
			if !seen {
				t.Errorf("stub argv missing %q; got %v", flag, argv)
			}
		}

		gotStdin, err := os.ReadFile(stdinFile)
		if err != nil {
			t.Fatalf("read stub stdin: %v", err)
		}
		if string(gotStdin) != sampleStdin {
			t.Errorf("stub stdin = %q, want %q", gotStdin, sampleStdin)
		}
	})

	t.Run("nonzero tool exit blocks push", func(t *testing.T) {
		if err := os.WriteFile(exitFile, []byte("1\n"), 0o600); err != nil {
			t.Fatalf("write exitcode: %v", err)
		}

		//nolint:gosec // G204: path is examples/pre-push from the repo; args are test literals.
		cmd := exec.Command(path, "origin", "git@github.com:example/repo.git")
		cmd.Env = env
		cmd.Stdin = strings.NewReader(sampleStdin)
		err := cmd.Run()
		if err == nil {
			t.Fatal("examples/pre-push exited 0 when tool exited 1; push would not be blocked")
		}
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("unexpected error type: %v", err)
		}
		if ee.ExitCode() == 0 {
			t.Fatal("exit code 0 when tool failed")
		}
	})
}
