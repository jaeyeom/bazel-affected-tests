# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.5.0] - 2026-04-22

### Added

- `--max-parent-depth` flag and `max_parent_depth` config key to cap how
  many parent directories are walked looking for a BUILD file
- `--strict` flag and `strict` config key to fail when any changed file
  does not map to a Bazel package within the depth cap

### Changed

- **Breaking:** `FindBazelPackage` no longer walks an unbounded number of
  parent directories. The default cap is 1, so a changed file that is more
  than one directory below the nearest BUILD file is now logged as
  unmapped and skipped. This prevents the tool from silently resolving to
  a very broad package (e.g. `//`) and pulling in the entire workspace's
  tests. To restore the previous behavior, pass `--max-parent-depth=-1`
  or set `max_parent_depth: -1` in `.bazel-affected-tests.yaml`.

### Fixed

- Bazel internal crashes (e.g., JVM-level exceptions during external repository
  fetch) now degrade gracefully: the crashing query is logged as a warning,
  partial results are returned, and the process exits 0 instead of forcing
  callers into a full test fallback

## [v0.4.0] - 2026-04-12

### Added

- `--run` flag to execute affected tests directly via `bazel test`
- `--staged`, `--head`, `--base` flags with auto-detect mode for flexible input
- Configurable sub-package test query (`enable_subpackage_query`)
- `--keep_going`, `--nohost_deps`, `--noimplicit_deps` flags on rdeps query

### Changed

- Renamed `BAZEL_AFFECTED_TESTS_FAIL_ON_ERROR` environment variable to `BAZEL_AFFECTED_TESTS_BEST_EFFORT` (inverted sense: set to `true` for lenient mode)
- Bumped `go-cmdexec` dependency to v0.3.0 (includes LICENSE file for distribution)

### Fixed

- Config-only targets are now returned even when changed files have no Bazel package
- Config version field is now validated (rejects unsupported versions)
- Bazel query and config errors are fatal by default (use `--best-effort` for lenient mode)
- Removed `--noblock_for_lock` flag dropped in Bazel 8.x
- Repo root is now resolved via `git rev-parse` instead of working directory

## [v0.3.1] - 2026-04-01

### Fixed

- Skip sub-package query for root package to avoid matching all tests
- Fix pre-commit hook example to actually run affected tests

## [v0.3.0] - 2026-03-31

### Added

- Sub-package test discovery (`kind('.*_test rule', PKG/...)`)
- Piped stdin support for file input
- `--files-from` flag replacing implicit stdin detection
- `ignore_paths` config field to skip non-Bazel files before package resolution

### Changed

- Updated golangci-lint-action to v7 for v2 config compatibility

## [v0.2.1] - 2026-02-25

### Added

- Exclude support in configuration file for filtering query results
- `exclude` field in `.bazel-affected-tests.yaml` uses `path.Match` syntax on
  Bazel target labels

## [v0.2.0] - 2026-02-25

### Changed

- Updated `go-cmdexec` dependency to v0.2.0
- Removed hardcoded format test filter in favor of configuration-driven approach

### Improved

- Test quality improvements from code review findings

## [v0.1.0] - 2026-02-13

### Added

- Initial release of `bazel-affected-tests` CLI tool
- Staged file detection via `git diff --cached`
- Bazel package discovery by walking directory tree for BUILD files
- Two-query strategy per package: same-package tests and reverse-dependency tests
- SHA-256 based caching keyed on BUILD and `.bzl` file content
- WORKSPACE/MODULE files intentionally excluded from cache key
- Configuration file support (`.bazel-affected-tests.yaml`) for pattern-based
  target rules
- `--debug`, `--cache-dir`, `--clear-cache`, and `--no-cache` CLI flags
- `BAZEL_AFFECTED_TESTS_FAIL_ON_ERROR` environment variable

[Unreleased]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.5.0...HEAD
[v0.5.0]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.4.0...v0.5.0
[v0.4.0]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.3.1...v0.4.0
[v0.3.1]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.3.0...v0.3.1
[v0.3.0]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.2.1...v0.3.0
[v0.2.1]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/jaeyeom/bazel-affected-tests/releases/tag/v0.1.0
