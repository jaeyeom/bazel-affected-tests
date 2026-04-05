# bazel-affected-tests

## Build / Test / Run

- `make check` is the CI-equivalent command (format check + lint + test + build)
- `make all` auto-fixes formatting and lint issues before testing and building
- `golangci-lint` v2 is required — v1 config is incompatible
- Build and test commands need sandbox disabled due to Go build cache paths

## Architecture

- `internal/query` owns all Bazel query interactions — other packages must not shell out to `bazel` directly
- `internal/config` parses `.bazel-affected-tests.yaml` — see `examples/` for the config schema
- Input auto-detection order (no flags): piped stdin > staged files > HEAD diff. Explicit flags (`--staged`, `--head`, `--base`, `--files-from`) are mutually exclusive with each other
- Cache key is derived from the repo's Bazel workspace state so it invalidates when BUILD files change

## Conventions

- Tests use only the standard library — no testify or external test frameworks
- Error wrapping uses `fmt.Errorf("...: %w", err)` consistently
- The `go-cmdexec` dependency provides a testable executor interface — use it instead of `os/exec` directly

## Git Hooks

- This repo uses [gabyx/Githooks](https://github.com/gabyx/Githooks), not standard git hooks
- Shared hooks come from `jaeyeom/shared-githooks` (pre-commit: format, lint, large file checks; commit-msg: subject length, co-authored-by validation)
- If a commit fails with "untrusted hook" errors, trust via pattern first:
  `git hooks trust hooks --pattern 'ns:jaeyeom-shared-githooks/**'`
- Never skip untrusted hooks (`git hooks config skip-untrusted-hooks --enable` is forbidden)
- Hook execution requires sandbox disabled
