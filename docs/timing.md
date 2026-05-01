# Timing / Profiling

`bazel-affected-tests` can print a per-stage wall-clock breakdown to **stderr**
when invoked with `--timing` (alias `--profile`). Use it to identify which
phase dominates on your repository. Stdout (the affected target list) is not
affected, so `--timing` composes safely with pipelines and `--run`.

## Example

```text
$ bazel-affected-tests --timing --base origin/main
//pkg/foo:foo_test
//pkg/bar:bar_test
timing:
  repo-root       4.435ms  (  3.2%)
  changed-files   5.857ms  (  4.2%)
  load-config        13µs  (  0.0%)
  find-packages      24µs  (  0.0%)
  cache-key      85.220ms  ( 60.7%)
  bazel-query    44.870ms  ( 32.0%)
  total         140.430ms
```

The percentage column shows each stage's share of the **total** wall-clock
time. Stages do not overlap and are not exhaustive — small amounts of work
between stages (config flag resolution, ignore-path filtering, dedup, output)
are counted in `total` but not attributed to any stage. So the per-stage
percentages will typically sum to slightly less than 100%.

## Stages

### `repo-root`

Resolves the repository root with `git rev-parse --show-toplevel`. Pure
subprocess + libgit overhead; on healthy filesystems this is a few
milliseconds.

### `changed-files`

Detects the input set of changed files. The exact source depends on flags:

- `--files-from <path>` — reads lines from a file (or stdin via `-`)
- `--staged` — `git diff --cached --name-only`
- `--head` — `git diff HEAD --name-only` (staged + unstaged)
- `--base <ref>` — `git diff <ref> --name-only`
- otherwise (auto): piped stdin → staged → HEAD

Cost is dominated by the `git diff` invocation against your working tree.

### `load-config`

Parses `.bazel-affected-tests.yaml` from the repo root (if present). Pure
file read + YAML parse. Typically negligible (microseconds).

### `find-packages`

For each changed file, walks up the directory tree looking for a `BUILD` or
`BUILD.bazel`, capped at `--max-parent-depth`. Cost scales with the number of
changed files times the depth cap; usually fast because each file resolves
within a few `os.Stat` calls.

### `cache-key`

Walks the entire repo for `BUILD`, `BUILD.bazel`, and `*.bzl` files, reads
each one, and computes a SHA-256 over the sorted paths and contents. This
key invalidates cached `bazel query` results when any build file changes.

This stage is **I/O-bound** and tends to dominate on large monorepos —
expect it to scale with the number and size of build files. It is the most
useful stage to monitor when investigating slow first-run performance.

Skipped when `--no-cache` is set or when no Bazel packages were found.

### `bazel-query`

Runs `bazel query` once per affected package to enumerate test targets,
storing each result in the on-disk cache. Three sub-queries fire per package
(same-package tests, sub-package tests, reverse-dep tests). When the cache
is warm and the cache key has not changed, this stage is mostly cache hits
and finishes in microseconds; on a cold cache it is dominated by Bazel
startup + analysis time.

Skipped when no Bazel packages were found.

### `total`

Wall-clock from `--timing` setup (just after flag parsing) to the moment
the report is printed. Includes the small amounts of work between stages
that are not individually instrumented.

## Tips

- Persistent slow `cache-key` → consider whether your repo has many large
  generated `*.bzl` files, or whether the cache directory itself is on slow
  storage.
- Persistent slow `bazel-query` → check whether the cache is being
  invalidated each run (e.g. files modified that change the cache key) or
  whether Bazel itself is doing expensive analysis.
- Compose with `time` to capture full process wall-clock including Go
  startup: `time bazel-affected-tests --timing --head`.
- Use `--no-cache` to force a cold path through `bazel-query` for
  apples-to-apples comparisons.
