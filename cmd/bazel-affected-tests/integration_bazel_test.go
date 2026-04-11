package main

import (
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestCLI_RealBazel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("skipping: test uses shell scripts not supported on Windows")
	}
	if _, err := exec.LookPath("bazel"); err != nil {
		t.Skip("skipping: bazel not found in PATH")
	}

	root := repoRoot(t)
	binDir := t.TempDir()
	binPath := binDir + "/bazel-affected-tests"
	runCommand(t, root, nil, "go", "build", "-o", binPath, "./cmd/bazel-affected-tests")

	wsDir := t.TempDir()

	// Initialize git repo (required for the tool to find repo root).
	runCommand(t, wsDir, nil, "git", "init")
	runCommand(t, wsDir, nil, "git", "config", "user.email", "test@test.com")
	runCommand(t, wsDir, nil, "git", "config", "user.name", "Test")

	// Create a minimal Bazel workspace using custom Starlark rules so
	// the test has zero external dependencies (sh_test/sh_library moved
	// to @rules_shell in Bazel 8+ and are absent in Bazel 9).
	writeFile(t, wsDir+"/MODULE.bazel", `module(name = "test_workspace")`+"\n", 0o644)
	writeFile(t, wsDir+"/BUILD.bazel", "", 0o644)

	// Custom rules that work in any Bazel version.
	writeFile(t, wsDir+"/defs.bzl", strings.TrimSpace(`
"""Minimal custom rules for integration testing."""

def _dummy_library_impl(ctx):
    return [DefaultInfo(files = depset(ctx.files.srcs))]

dummy_library = rule(
    implementation = _dummy_library_impl,
    attrs = {
        "srcs": attr.label_list(allow_files = True),
    },
)

def _dummy_test_impl(ctx):
    script = ctx.actions.declare_file(ctx.label.name + ".sh")
    ctx.actions.write(script, "#!/bin/sh\nexit 0\n", is_executable = True)
    return [DefaultInfo(executable = script)]

dummy_test = rule(
    implementation = _dummy_test_impl,
    test = True,
    attrs = {
        "deps": attr.label_list(),
    },
)
`)+"\n", 0o644)

	// pkg/lib: a library with its own test.
	writeFile(t, wsDir+"/pkg/lib/BUILD.bazel", strings.TrimSpace(`
load("//:defs.bzl", "dummy_library", "dummy_test")

dummy_library(
    name = "lib",
    srcs = ["lib.sh"],
    visibility = ["//visibility:public"],
)

dummy_test(
    name = "lib_test",
    deps = [":lib"],
)
`)+"\n", 0o644)
	writeFile(t, wsDir+"/pkg/lib/lib.sh", "#!/bin/sh\necho lib\n", 0o755)

	// pkg/consumer: a test that depends on //pkg/lib.
	writeFile(t, wsDir+"/pkg/consumer/BUILD.bazel", strings.TrimSpace(`
load("//:defs.bzl", "dummy_test")

dummy_test(
    name = "consumer_test",
    deps = ["//pkg/lib"],
)
`)+"\n", 0o644)

	// Initial commit so that git diff --cached works correctly.
	runCommand(t, wsDir, nil, "git", "add", "-A")
	runCommand(t, wsDir, nil, "git", "commit", "-m", "initial")

	// Modify a file in pkg/lib and stage it.
	writeFile(t, wsDir+"/pkg/lib/lib.sh", "#!/bin/sh\necho updated lib\n", 0o755)
	runCommand(t, wsDir, nil, "git", "add", "pkg/lib/lib.sh")

	// Run the tool with --staged --no-cache.
	output := runCommand(t, wsDir, nil, binPath, "--staged", "--no-cache")

	got := strings.Split(strings.TrimSpace(output), "\n")
	sort.Strings(got)

	want := []string{
		"//pkg/consumer:consumer_test",
		"//pkg/lib:lib_test",
	}

	if len(got) != len(want) {
		t.Fatalf("got %d targets %v, want %d targets %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
