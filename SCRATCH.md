# Public Release Preparation

Checklist and notes for preparing `bazel-affected-tests` for public release.

## Critical

### 1. ~~Add LICENSE file~~ (DONE)

No license file exists. Without one, the code is "All Rights Reserved" by
default and cannot legally be used by others.

**Options:**
- MIT — permissive, minimal restrictions
- Apache 2.0 — permissive with patent grant
- BSD 3-Clause — permissive, similar to MIT

**Action:** Create `LICENSE` in repo root with chosen license text.

---

## High Priority

### 2. ~~Add CI/CD (GitHub Actions)~~ (DONE)

No `.github/workflows/` directory exists. No automated testing on PRs, no
release workflow.

**Action:** Create `.github/workflows/ci.yml`:
- Trigger on push and PR to `main`
- Matrix: Go 1.24.x on ubuntu-latest (optionally macos-latest)
- Steps: checkout, setup-go, `make check` (runs format check + lint + vet +
  test + build)
- Optionally add `go test -race ./...`
- Optionally add coverage upload (Codecov or Coveralls)

**Action:** Create `.github/workflows/release.yml`:
- Trigger on tag push (`v*`)
- Use GoReleaser or manual `go build` to produce binaries
- Publish GitHub Release with changelog

### 3. ~~Add CHANGELOG.md~~ (DONE)

Three versions tagged (`v0.1.0`, `v0.2.0`, `v0.2.1`) with no release notes.

**Action:** Create `CHANGELOG.md` documenting changes for each version.
Reconstruct from git log:

```
git log --oneline v0.1.0..v0.2.0
git log --oneline v0.2.0..v0.2.1
```

### 4. ~~Fix `GetCacheKey()` CWD dependency~~ (DONE)

**File:** `internal/cache/cache.go:38`

`filepath.Walk(".")` walks from the process's current working directory. If
invoked from a subdirectory, it produces a wrong cache key.

**Fix:** Accept repo root as a parameter:

```go
// Before
func (c *Cache) GetCacheKey() (string, error) {
    err := filepath.Walk(".", func(...) { ... })

// After
func (c *Cache) GetCacheKey(repoRoot string) (string, error) {
    err := filepath.Walk(repoRoot, func(...) { ... })
```

Same issue applies to `FindBazelPackage` in `internal/query/package.go:26-28`.

### 5. ~~Fix silent error swallowing in `GetCacheKey()`~~ (DONE)

**File:** `internal/cache/cache.go:40,67`

Unreadable BUILD files are silently skipped, producing incomplete cache keys.

**Fix:** Add `slog.Warn` for skipped files:

```go
// Line 40: filepath.Walk callback
if err != nil {
    slog.Warn("Skipping inaccessible path in cache key", "path", path, "error", err)
    return nil
}

// Line 67: file open loop
f, err := os.Open(file)
if err != nil {
    slog.Warn("Skipping unreadable file in cache key", "file", file, "error", err)
    continue
}
```

### 6. ~~Remove global slog mutation from constructors~~ (DONE)

**Files:**
- `internal/cache/cache.go:23`
- `internal/query/bazel.go:23`
- `internal/query/bazel.go:37`

`slog.SetLogLoggerLevel(slog.LevelDebug)` is called inside `NewCache`,
`NewBazelQuerier`, and `NewBazelQuerierWithExecutor`. This is a global side
effect hidden inside constructors. It also causes non-deterministic test output.

**Fix:** Remove `debug` parameter from constructors. Set log level only once in
`main()` (which already does this at `main.go:22`).

### 7. ~~Add tests for `internal/git` package~~ (DONE)

**File:** `internal/git/git.go`

`GetStagedFiles` has zero tests despite accepting an `executor.Executor`
interface (easily mockable).

**Action:** Create `internal/git/git_test.go` with tests covering:
- Empty output (no staged files)
- Single file
- Multiple files
- Output with trailing newlines
- Error from executor
- Filtering empty strings from split

---

## Medium Priority

### 8. ~~Add `.claude/` and `.omc/` to `.gitignore`~~ (DONE)

These directories are excluded by global gitignore but contributors won't have
that configuration.

**Action:** Append to `.gitignore`:

```
# Claude/OMC tooling state
.claude/
.omc/
```

### 9. ~~Guard against cache path traversal~~ (DONE)

**File:** `internal/cache/cache.go:123-132`

`getCacheFile` doesn't guard against `..` in package names. A package name like
`//..` would become `..` after sanitization.

**Fix:** Validate that resolved path stays within cache directory:

```go
func (c *Cache) getCacheFile(cacheKey, pkg string) string {
    safePkg := strings.ReplaceAll(pkg, "//", "")
    safePkg = strings.ReplaceAll(safePkg, "/", "__")
    safePkg = strings.ReplaceAll(safePkg, ":", "__")
    if safePkg == "" {
        safePkg = "root"
    }
    result := filepath.Join(c.dir, cacheKey, safePkg+".json")
    // Guard: ensure path stays within cache directory
    if !strings.HasPrefix(filepath.Clean(result), filepath.Clean(c.dir)) {
        return filepath.Join(c.dir, cacheKey, "invalid.json")
    }
    return result
}
```

### 10. ~~Replace `os.Exit` in `handleCacheClear` with error return~~ (DONE)

**File:** `cmd/bazel-affected-tests/main.go:98,103`

`os.Exit(0)` and `os.Exit(1)` inside `handleCacheClear` make the function
untestable.

**Fix:**

```go
// Before
func handleCacheClear(c *cache.Cache, debug bool) {
    if err := c.Clear(); err != nil {
        fmt.Fprintf(os.Stderr, "Error clearing cache: %v\n", err)
        os.Exit(1)
    }
    if debug {
        fmt.Println("Cache cleared successfully")
    }
    os.Exit(0)
}

// After
func handleCacheClear(c *cache.Cache) error {
    if err := c.Clear(); err != nil {
        return fmt.Errorf("clearing cache: %w", err)
    }
    slog.Debug("Cache cleared successfully")
    return nil
}
```

### 11. ~~Remove `os.Chdir` usage in tests~~ (DONE)

**Files:**
- `internal/cache/cache_test.go:53-60`
- `internal/config/config_test.go:57-69,92-103`
- `internal/query/package_test.go:21-28`

`os.Chdir()` mutates process-global state, preventing parallel test execution.

**Fix:** This is a consequence of items #4 (GetCacheKey CWD) and similar CWD
dependencies in `LoadConfig` and `FindBazelPackage`. Refactoring those functions
to accept explicit root paths eliminates the need for `os.Chdir` in tests.

### 12. ~~Fix OS detection in test~~ (DONE)

**File:** `internal/cache/cache_test.go:454`

```go
// Before (wrong — GOOS env var is only set during cross-compilation)
if os.Getenv("GOOS") == "windows" {

// After (correct)
if runtime.GOOS == "windows" {
```

### 13. ~~Validate Bazel package labels before query interpolation~~ (DONE)

**File:** `internal/query/bazel.go:66,80`

Package names are interpolated into Bazel query strings. While currently derived
from filesystem paths (not direct user input), validating the format adds defense
in depth.

**Fix:**

```go
var validPkgPattern = regexp.MustCompile(`^//[a-zA-Z0-9_./-]*$`)

func validatePackageLabel(pkg string) error {
    if !validPkgPattern.MatchString(pkg) {
        return fmt.Errorf("invalid bazel package label: %q", pkg)
    }
    return nil
}
```

---

## Low Priority / Polish

### 14. Add README badges

Add to top of `README.md`:

```markdown
[![CI](https://github.com/jaeyeom/bazel-affected-tests/actions/workflows/ci.yml/badge.svg)](https://github.com/jaeyeom/bazel-affected-tests/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/jaeyeom/bazel-affected-tests)](https://goreportcard.com/report/github.com/jaeyeom/bazel-affected-tests)
[![Go Reference](https://pkg.go.dev/badge/github.com/jaeyeom/bazel-affected-tests.svg)](https://pkg.go.dev/github.com/jaeyeom/bazel-affected-tests)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
```

### 15. Add example config file

Commit an example configuration so users can copy-paste:

```
examples/.bazel-affected-tests.yaml
```

### 16. Add CONTRIBUTING.md

Cover:
- Prerequisites (Go 1.24+, Bazel, golangci-lint)
- How to build (`make build`)
- How to test (`make test`)
- How to lint (`make check`)
- PR process
- Code style (already enforced by linters)

### 17. Add .goreleaser.yml

Automate binary releases for tagged versions. Produces cross-platform binaries
and attaches them to GitHub Releases.

### 18. Minor code fixes

- **Replace custom `contains` with `strings.Contains`**
  `internal/query/bazel_test.go:677-688` — hand-rolled helper is unnecessary.

- **Handle `os.UserHomeDir()` error**
  `internal/cache/cache.go:27` — currently `homeDir, _ := os.UserHomeDir()`
  silently discards error.

- **Use `slog.Debug` instead of `fmt.Println` for debug message**
  `cmd/bazel-affected-tests/main.go:101` — inconsistent with rest of codebase.

- **Use `t.TempDir()` instead of `os.MkdirTemp`**
  `internal/cache/cache_test.go:43,280,346,459,489` and
  `internal/query/package_test.go:11` — idiomatic Go testing, auto-cleanup.

- **Consider `filepath.WalkDir` over `filepath.Walk`**
  `internal/cache/cache.go:38` — more efficient (Go 1.16+), avoids `os.Stat` on
  every entry.

- **Use `0o700` for cache directories**
  `internal/cache/cache.go:98` — currently `0o755` (world-readable). `0o700` is
  more appropriate for user cache.

- **Add godoc to exported `Config` struct fields**
  `internal/config/config.go:18-28` — `Version`, `Exclude`, `Rules`, `Patterns`,
  `Targets` all lack field documentation.

---

## Summary

| Priority | Count | Items |
|----------|-------|-------|
| Critical | 1 | LICENSE |
| High | 6 | CI/CD, CHANGELOG, CWD fix, silent errors, slog mutation, git tests |
| Medium | 6 | .gitignore, path traversal, os.Exit, os.Chdir, OS detection, label validation |
| Low | 5 | Badges, example config, CONTRIBUTING, goreleaser, minor code fixes |
| **Total** | **18** | |
