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
- `--base <ref>`: Use all changes vs a ref (`git diff <ref>`)
- `--files-from <path>`: Read changed file list from a file (use `-` for stdin)
- `--run`: Run `bazel test` with the affected targets instead of printing them
- `--best-effort`: Log warnings instead of failing on Bazel query errors (also via env `BAZEL_AFFECTED_TESTS_BEST_EFFORT=true`)

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

# Combine with other flags
bazel-affected-tests --run --staged
bazel-affected-tests --run --base main

# Use xargs (note: add -r on GNU/Linux to avoid running bazel with no targets)
bazel-affected-tests | xargs -r bazel test

# Read changed files from stdin
git diff --name-only main | bazel-affected-tests --files-from -

# Read changed files from a file
bazel-affected-tests --files-from changed_files.txt
```

### Integration with Pre-commit Hooks

Add to your pre-commit configuration:

```yaml
- id: bazel-affected-tests
  name: Run affected Bazel tests
  entry: bazel-affected-tests --run
  language: system
```

```yaml
# Alternative using xargs (works on macOS; add -r for GNU/Linux)
- id: bazel-affected-tests
  name: Run affected Bazel tests
  entry: sh -c 'targets=$(bazel-affected-tests) && [ -n "$targets" ] && echo "$targets" | xargs bazel test'
  language: system
```

Make sure to run `go install` first to ensure the binary is in your PATH.

## How It Works

1. **File Detection**: Determines changed files using this priority order:
   - `--files-from`, `--staged`, `--head`, or `--base` if explicitly given (mutually exclusive)
   - Otherwise, **auto-detection**: piped stdin → git staged files → `git diff HEAD` (staged + unstaged)
   - Only Added, Copied, and Modified files are included (not Deleted)
2. **Package Finding**: Finds the nearest Bazel package (directory with BUILD file) for each file
3. **Test Discovery**: Uses `bazel query` to find:
   - Test targets within the same package
   - External test targets that depend on the package
4. **Caching**: Results are cached based on BUILD and `.bzl` file content hashes
5. **Output**: Prints affected test targets, one per line

**Granularity**: This tool operates at **package-level granularity**, not file-level. A Bazel package is a directory containing a BUILD file. When any file in a package is modified, all tests that depend on that package are considered affected.

## Error Handling

By default, Bazel query failures and config parse errors are **fatal** — the tool exits with a nonzero status so CI pipelines and pre-commit hooks detect the problem. This prevents silently missing affected tests.

If you prefer a lenient mode (e.g., for local development where Bazel may not be fully available), use `--best-effort` or set `BAZEL_AFFECTED_TESTS_BEST_EFFORT=true`. In this mode, query failures are logged as warnings and the tool continues with partial results.

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

The `ignore_paths` field uses glob patterns on file paths to skip files entirely before package resolution. Files matching these patterns are excluded from all processing — no package lookup and no test discovery. This is useful for documentation, config files, or other non-code files that don't affect tests.

The `exclude` field uses `path.Match` syntax on Bazel target labels (e.g., `//tools/format:*` matches all targets in the `//tools/format` package). This is useful for filtering out targets that get discovered via `rdeps` queries but should only be included when explicitly matched by a rule.

### Use Cases

- **Buildifier**: Run buildifier checks when BUILD or .bzl files change
- **External tools**: Include external tool targets that depend on specific file types
- **Custom workflows**: Add any targets based on file patterns that can't be discovered via bazel query
