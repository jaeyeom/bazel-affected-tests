# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[v0.2.1]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/jaeyeom/bazel-affected-tests/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/jaeyeom/bazel-affected-tests/releases/tag/v0.1.0
