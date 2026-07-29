# Bazel Affected Tests

[![CI](https://github.com/jaeyeom/bazel-affected-tests/actions/workflows/ci.yml/badge.svg)](https://github.com/jaeyeom/bazel-affected-tests/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/jaeyeom/bazel-affected-tests)](https://goreportcard.com/report/github.com/jaeyeom/bazel-affected-tests)
[![Go Reference](https://pkg.go.dev/badge/github.com/jaeyeom/bazel-affected-tests.svg)](https://pkg.go.dev/github.com/jaeyeom/bazel-affected-tests)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A fast Go implementation of the Bazel affected tests detection tool. This tool
identifies which Bazel test targets might be affected by changes in your git
staging area.

## Features

- **Fast**: 10-100x faster than shell implementation with caching
- **Smart Caching**: Caches results based on BUILD file content hashes
- **Cross-platform**: Works on Linux and macOS (Windows may work but is untested)
- **Config File Support**: Add custom targets based on file patterns (e.g., format tests)
- **Debug Mode**: Detailed output for troubleshooting
- **Built-in execution**: Run affected tests directly with `--run` — no `xargs` needed

## Installation

```bash
go install github.com/jaeyeom/bazel-affected-tests/cmd/bazel-affected-tests@latest
```

## Usage

### Basic Usage

```bash
# Run affected tests directly (recommended)
bazel-affected-tests --run

# Or just list affected test targets
bazel-affected-tests

# Pipe to xargs (add -r on GNU/Linux to skip empty input)
bazel-affected-tests | xargs -r bazel test
```

### Command Line Options

- `--debug` or env `DEBUG=1`: Enable debug output
- `--cache-dir`: Custom cache directory (default: `$HOME/.cache/bazel-affected-tests`)
- `--clear-cache`: Clear the cache and exit
- `--no-cache`: Disable caching for this run
- `--staged`: Use staged files only (`git diff --cached`)
- `--head`: Use staged + unstaged files (`git diff HEAD`)
- `--base <ref>`: Use all changes vs a ref (`git diff <ref>` against the working tree)
- `--diff <old>..<new>` / `--diff <old>...<new>`: Files differing between two tree-ishes (no working tree). Two-dot is tip-to-tip; three-dot is PR-scope (`git diff A...B`)
- `--pre-push`: Read git pre-push stdin (`local_ref local_oid remote_ref remote_oid`) and select files from the push range (PR-scope vs the integration branch by default)
- `--pre-push-base <ref>`: With `--pre-push`, preferred integration branch for PR-scope (default: first of `origin/main`, `main`, `origin/master`, `master` that exists)
- `--pre-push-tip`: With `--pre-push`, use tip-to-tip (`remote_oid..local_oid`) instead of PR-scope; new branches still fall back to PR-scope
- `--files-from <path>`: Read changed file list from a file (use `-` for stdin)
- `--run`: Run `bazel test` with the affected targets instead of printing them
- `--fail-fast`: With `--run`, stop after the first test failure (passes Bazel `--test_keep_going=false` and `--test_runner_fail_fast`). Prefer this for hooks when latency matters more than reporting every failing target; has no effect when only listing targets
- `--test-size <sizes>`: Comma-separated Bazel test sizes to keep after affected selection (`small`, `medium`, `large`, `enormous`). Applies to both list and `--run` modes (overrides `test_size_filters` in the config file). Prefer this for hooks when you want fast small/medium feedback and leave large/enormous coverage to CI. Default when omitted is no size filter (all sizes). Tests with no explicit `size` typically default to `medium` in Bazel.
- `--best-effort`: Log warnings instead of failing on Bazel query errors (also via env `BAZEL_AFFECTED_TESTS_BEST_EFFORT=true` or `best_effort` in the config file)
- `--query-timeout <dur>`: Per-Bazel-query wall-clock limit, e.g. `60s` or `2m` (overrides `query_timeout` in the config file; default `30s`). Raise this for large monorepos whose `rdeps` queries traverse a big graph.
- `--max-parent-depth <n>`: Cap how many parent directories above a changed file's own directory may be walked looking for a BUILD file (default `1`; pass `-1` for unlimited). Files that don't resolve within the cap are logged as a warning and skipped.
- `--strict`: Fail with a non-zero exit if any changed file does not map to a Bazel package within `--max-parent-depth` (after `ignore_paths` filtering). Useful in CI to catch forgotten BUILD entries.

### Examples

```bash
# Run with debug output
bazel-affected-tests --debug

# Clear cache
bazel-affected-tests --clear-cache

# Run without cache
bazel-affected-tests --no-cache

# Run affected tests directly (no xargs needed)
bazel-affected-tests --run

# Stop after the first test failure (faster feedback for hooks)
bazel-affected-tests --run --fail-fast

# Only small and medium tests (skip large/enormous for local speed)
bazel-affected-tests --test-size small,medium
bazel-affected-tests --run --test-size small,medium --fail-fast

# Combine with other flags
bazel-affected-tests --run --staged
bazel-affected-tests --run --base main

# Tree-ish range (clean tree / push-range friendly; no working tree)
bazel-affected-tests --diff origin/main...HEAD
bazel-affected-tests --run --diff "$remote_oid..$local_oid"

# Use xargs (note: add -r on GNU/Linux to avoid running bazel with no targets)
bazel-affected-tests | xargs -r bazel test

# Read changed files from stdin
git diff --name-only main | bazel-affected-tests --files-from -

# Read changed files from a file
bazel-affected-tests --files-from changed_files.txt
```

### Integration with Pre-commit Hooks

Pre-commit should use the **working tree / index** (what you are about to commit). Soft local gates may use `--best-effort`; CI remains the authoritative full suite.

Add to your pre-commit configuration:

```yaml
- id: bazel-affected-tests
  name: Run affected Bazel tests
  entry: bazel-affected-tests --run --staged --fail-fast
  language: system
```

```yaml
# Alternative using xargs (works on macOS; add -r for GNU/Linux)
- id: bazel-affected-tests
  name: Run affected Bazel tests
  entry: sh -c 'targets=$(bazel-affected-tests --staged) && [ -n "$targets" ] && echo "$targets" | xargs bazel test'
  language: system
```

Make sure to run `go install` first to ensure the binary is in your PATH.

### Integration with Pre-push Hooks

Pre-push must use the **commits being pushed**, not a clean working tree. Use `--pre-push` so the tool reads git’s pre-push stdin protocol and diffs tree-ishes (PR-scope by default, which is correct after rebase + force-with-lease).

**Do not** use auto mode, `--staged`, or `--head` on pre-push: with a clean tree they select no files and the push succeeds without running tests. **Do not** enable `--best-effort` for a blocking pre-push gate (query timeouts under-select and still exit 0).

Example hook (`examples/pre-push`):

```bash
#!/usr/bin/env bash
# pre-push: run affected tests for the push range (PR-scope).
set -euo pipefail
exec bazel-affected-tests --pre-push --run --fail-fast
```

Install as `.git/hooks/pre-push` (or via your hook manager) and ensure it is executable. Optional:

```bash
# Prefer a specific integration branch for PR-scope
bazel-affected-tests --pre-push --pre-push-base origin/develop --run --fail-fast

# Tip-to-tip only (what changed since the remote tip; weaker after rebase)
bazel-affected-tests --pre-push --pre-push-tip --run --fail-fast
```

| Hook | File source | Suggested flags | Best-effort |
|------|-------------|-----------------|-------------|
| pre-commit | `--staged` (or auto) | `--run --fail-fast` (+ optional `--test-size small,medium`) | optional |
| pre-push | `--pre-push` | `--run --fail-fast` | **off** (fail closed) |

## How It Works

1. **File Detection**: Determines changed files using this priority order:
   - `--files-from`, `--staged`, `--head`, `--base`, `--diff`, or `--pre-push` if explicitly given (mutually exclusive)
   - Otherwise, **auto-detection**: piped stdin → git staged files → `git diff HEAD` (staged + unstaged)
   - Only Added, Copied, and Modified files are included (not Deleted)
   - `--pre-push` and `--diff` operate on commit tree-ishes (not the working tree)
2. **Package Finding**: Finds the nearest Bazel package (directory with BUILD file) for each file
3. **Test Discovery**: Uses `bazel query` to find:
   - Test targets within the same package
   - External test targets that depend on the package
4. **Caching**: Results are cached based on BUILD and `.bzl` file content hashes
5. **Output**: Prints affected test targets, one per line

**Granularity**: This tool operates at **package-level granularity**, not file-level. A Bazel package is a directory containing a BUILD file. When any file in a package is modified, all tests that depend on that package are considered affected.

## Error Handling

By default, Bazel query failures and config parse errors are **fatal** — the tool exits with a nonzero status so CI pipelines and pre-commit hooks detect the problem. This prevents silently missing affected tests.

If you prefer a lenient mode (e.g., for local **pre-commit** feedback where Bazel may not be fully available), use `--best-effort`, set `BAZEL_AFFECTED_TESTS_BEST_EFFORT=true`, or set `best_effort: true` in the config file. In this mode, query failures are logged as warnings and the tool continues with partial results. The CLI flag wins over the config file, which wins over the environment variable. Enabling it repo-wide via the config file is appropriate when this run is only a soft filter and an authoritative downstream gate (CI/CD) runs the full suite — note that best-effort *under-selects* on a timed-out broad query rather than falling back to running everything. Prefer **strict** mode (default) for **blocking pre-push** hooks so a timed-out query fails the push instead of silently under-selecting.

**Bazel internal crashes** (e.g., JVM-level exceptions inside the Bazel server while fetching an external repository) are always treated as non-fatal, even without `--best-effort`. A crash is a signal that Bazel itself is broken rather than that tests are failing — propagating it would force callers (e.g., pre-commit hooks) to fall back to running the full test set, which would just re-trigger the same crash. Instead, the crashing query is logged as a warning and whatever results were computable from other queries are returned.

## Performance

- **First Run**: Similar to shell script (builds cache)
- **Subsequent Runs**: 10-100x faster (uses cache)
- **Cache Invalidation**: Automatic when BUILD or `.bzl` files change

## Implementation Details

### Package Structure

- `cmd/bazel-affected-tests/`: Main CLI application
- `internal/cache/`: Cache management with BUILD and `.bzl` file hashing
- `internal/config/`: Configuration file loading and pattern matching
- `internal/git/`: Git operations for staged files
- `internal/query/`: Bazel query execution and package finding

### Cache Management

The cache is stored in `$HOME/.cache/bazel-affected-tests/` by default. You can
customize this location using the `--cache-dir` flag.

The cache key is a SHA-256 hash of BUILD and `.bzl` files in the repository. This ensures proper cache invalidation when:
- BUILD files change (affecting which targets exist and their dependencies)
- `.bzl` files change (affecting macros/rules that generate targets)

**What's NOT included:** WORKSPACE and MODULE files are intentionally excluded because they define external dependencies and don't affect the internal dependency graph between your packages.

Cache structure:
```
~/.cache/bazel-affected-tests/
└── <sha256-hash>/          # Hash of all BUILD and .bzl files
    ├── root.json           # Cache for root package (//)
    ├── src.json            # Cache for //src package
    └── src__lib.json       # Cache for //src/lib package
```

## Design

This tool uses **package-level granularity** to identify affected tests. A Bazel package is a directory containing a BUILD file. When any file in a package is modified, the tool finds all tests affected by changes to that entire package.

It queries for:
1. Test targets within the same package as modified files (using `kind('.*_test rule', //package:*)`)
2. External test targets that depend on those packages (using `rdeps(//..., //package:*)`)

### Caching System

The caching system stores results **per package** using a hash of all BUILD and `.bzl` files as the cache key.

**Cache Key Computation**: SHA-256 hash of the paths and contents of all:
- BUILD and BUILD.bazel files (defines which targets exist and their dependencies)
- `.bzl` files (defines macros/rules that can generate targets and affect dependencies)

**What's excluded and why:**
- WORKSPACE and WORKSPACE.bazel files (define external dependencies, not internal dependency graph)
- MODULE and MODULE.bazel files (define external dependencies, not internal dependency graph)

Since the tool queries only within `//...` (your repository) to find which of your tests depend on your packages, external dependency configurations in WORKSPACE/MODULE files don't affect the results. Excluding them avoids unnecessary cache invalidation when updating external dependencies.

## Configuration File

You can create a `.bazel-affected-tests.yaml` file in the repository root to specify additional targets that should be included when certain files change. This is useful for targets that cannot be discovered via `bazel query`, such as external tools.

### Config File Format

```yaml
version: 1

# Skip files before package resolution (uses glob syntax)
ignore_paths:
  - "docs/**"
  - "**/*.md"
  - ".semgrep/**"

# Disable the sub-package test query (PKG/...) for more precise results.
# When true (default), tests in child packages are also discovered.
# Set to false if your rdeps queries are reliable and you want to avoid
# over-inclusion of unrelated sub-package tests.
enable_subpackage_query: false

# Cap how many parent directories to walk looking for a BUILD file.
# Default is 1. Use -1 for unlimited (walks to the repo root).
max_parent_depth: 1

# When true, fail if any changed file (after ignore_paths filtering) does
# not map to a Bazel package within max_parent_depth. Useful in CI to
# catch source files that were forgotten in a BUILD file.
strict: false

# Log Bazel query failures as warnings and continue with partial results
# instead of failing. Enable repo-wide only when an authoritative downstream
# gate (CI/CD) runs the full suite and this run is just a soft filter
# (e.g. pre-commit). Prefer false for blocking pre-push hooks.
# Overridden by --best-effort / BAZEL_AFFECTED_TESTS_BEST_EFFORT.
best_effort: false

# Per-Bazel-query wall-clock limit as a Go duration string (default 30s).
# Raise this for large monorepos whose rdeps queries traverse a big graph.
# Overridden by --query-timeout.
query_timeout: 30s

# Keep only tests of these Bazel sizes after affected selection.
# Useful for pre-push / local hooks that want fast feedback while CI runs
# large and enormous tests. Overridden by --test-size. Omitted = all sizes.
# test_size_filters:
#   - small
#   - medium

# Exclude targets discovered via bazel query (uses path.Match syntax)
exclude:
  - "//tools/format:*"

# Then selectively add back format tests based on file types
rules:
  - patterns:
      - "**/*.go"
    targets:
      - "//tools/format:format_test_Go_with_gofmt"
  - patterns:
      - "**/*.py"
    targets:
      - "//tools/format:format_test_Python_with_ruff"
  - patterns:
      - "**/*.cpp"
      - "**/*.cc"
      - "**/*.cxx"
      - "**/*.hpp"
      - "**/*.h"
    targets:
      - "//tools/format:format_test_C++_with_clang-format"
  - patterns:
      - "**/BUILD"
      - "**/BUILD.bazel"
      - "**/*.bzl"
    targets:
      - "//tools/format:format_test_Starlark_with_buildifier"
  - patterns:
      - "**/*.proto"
    targets:
      - "//tools/format:format_test_Protocol_Buffer_with_buf"
  - patterns:
      - "**/*.rs"
    targets:
      - "//tools/format:format_test_Rust_with_rustfmt"
  - patterns:
      - "**/*.yaml"
      - "**/*.yml"
    targets:
      - "//tools/format:format_test_YAML_with_yamlfmt"
```

### Pattern Syntax

The config file uses glob patterns to match files:

- `**` matches any number of directories (e.g., `**/BUILD` matches `foo/bar/BUILD`)
- `*` matches any characters within a single path component (e.g., `*.bzl` matches `defs.bzl`)
- Exact names match only that specific file (e.g., `WORKSPACE` matches only `WORKSPACE`, not `foo/WORKSPACE`)

### How It Works

1. Files matching `ignore_paths` patterns are removed before any processing
2. Targets matching `exclude` patterns are removed from query results
3. Each changed file is checked against the `rules` patterns
4. If any pattern matches, the corresponding targets are added to the output
5. Targets are deduplicated, so the same target won't appear twice
6. If `--test-size` / `test_size_filters` is set, only tests matching those
   Bazel sizes remain (applied after affected selection, not instead of it)

The `ignore_paths` field uses glob patterns on file paths to skip files entirely before package resolution. Files matching these patterns are excluded from all processing — no package lookup and no test discovery. This is useful for documentation, config files, or other non-code files that don't affect tests.

The `max_parent_depth` field caps how far the tool walks up the directory tree looking for a BUILD file. The default of `1` means a file in a subdirectory of a Bazel package still resolves correctly, but a file buried several directories below any BUILD file is treated as unmapped. This prevents the tool from silently resolving a file to a very broad package (e.g. `//`) and pulling in the entire workspace's tests. Set to `-1` to restore the pre-0.5 behavior of walking all the way to the repo root. Unmapped files are logged as a warning by default; set `strict: true` (or pass `--strict`) to fail the run instead.

The `exclude` field uses `path.Match` syntax on Bazel target labels (e.g., `//tools/format:*` matches all targets in the `//tools/format` package). This is useful for filtering out targets that get discovered via `rdeps` queries but should only be included when explicitly matched by a rule.

### Use Cases

- **Buildifier**: Run buildifier checks when BUILD or .bzl files change
- **External tools**: Include external tool targets that depend on specific file types
- **Custom workflows**: Add any targets based on file patterns that can't be discovered via bazel query
