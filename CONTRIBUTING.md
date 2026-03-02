# Contributing to bazel-affected-tests

Thank you for your interest in contributing!

## Prerequisites

- [Go](https://go.dev/dl/) 1.24 or later
- [golangci-lint](https://golangci-lint.run/welcome/install/) v2
- [Bazel](https://bazel.build/install) (for integration testing)

## Development

### Build

```bash
make build
```

### Run tests

```bash
make test
```

### Run all checks (CI equivalent)

```bash
make check
```

This runs formatting check, linting, tests, and build.

### Auto-fix formatting and lint issues

```bash
make all
```

## Code Style

- Code formatting is enforced by `gofmt`
- Linting rules are defined in `.golangci.yml`
- Tests use only the standard library (no testify or other test frameworks)
- Error wrapping uses `fmt.Errorf("...: %w", err)` for proper error chains

## Pull Request Process

1. Fork the repository and create your branch from `main`
2. Make your changes
3. Ensure `make check` passes
4. Submit a pull request

## Reporting Issues

Please open an issue on GitHub with a clear description of the problem and steps
to reproduce it.
