# Package Cohesion Audit

## Problem

`bazel-affected-tests` currently works at Bazel package granularity. A changed
file is mapped to the nearest directory containing a `BUILD` or `BUILD.bazel`
file, and every test that depends on any rule in that package may be selected.

This is conservative and simple, but it can be inefficient when a directory is
not cohesive. If one Bazel package contains many unrelated rules, a change to a
single file can pull sibling rules into the dependency surface even when those
rules do not own, use, or conceptually relate to the changed file.

The goal of this design is to add an advisory tool that detects such packages
and suggests where repository owners should split BUILD packages or rules.

This is intentionally separate from the main affected-test path. The normal
command should stay conservative, fast, and suitable for pre-commit and CI use.
The audit command can do deeper analysis, emit diagnostics, and guide
refactoring.

## Core Observation

For a changed file `f` in package `//pkg`, the current package-level model uses
`//pkg:*` as the affected surface.

For a more precise approximation, only the rules that own `f` should be used as
the starting surface:

```text
owner_rules(f) = rules in //pkg whose source-like attributes include f
```

The avoidable package-level cost is the extra dependency or test surface added
by unrelated sibling rules:

```text
waste(f) = surface(//pkg:*) - surface(owner_rules(f))
```

The audit should find packages where `waste(f)` is high for many files, or very
high for important files.

## Non-Goals

- Do not change the default affected-test semantics.
- Do not make rule-level affected-test selection the first implementation.
- Do not require an LLM for detection.
- Do not require perfect file ownership for generated files or complex macros.
- Do not automatically rewrite BUILD files.

## Proposed CLI

Initial command:

```bash
bazel-affected-tests audit-packages
```

Useful options:

```bash
bazel-affected-tests audit-packages //foo/...
bazel-affected-tests audit-packages --format=json
bazel-affected-tests audit-packages --metric=deps
bazel-affected-tests audit-packages --deep
bazel-affected-tests audit-packages --history=200
bazel-affected-tests audit-packages --fail-on-score=0.8
```

The default should run a static dependency amplification audit. Expensive
reverse dependency and history analysis should be opt-in.

## Metrics

### Dependency Amplification

This is the first metric to implement.

For every source-like file in a package:

```text
owner_dep_closure(f) = deps(owner_rules(f))
package_dep_closure(f) = deps(//pkg:*)
dependency_amplification(f) =
    len(package_dep_closure) / max(1, len(owner_dep_closure))
```

The package-level report aggregates this over files:

```text
median_dependency_amplification
p90_dependency_amplification
max_dependency_amplification
```

This directly captures the current inefficiency: a file change inherits the
dependency closure of unrelated sibling rules because they share one Bazel
package.

### Extra Dependency Count

Ratios can overstate small packages, so also report absolute extra dependencies:

```text
extra_deps(f) = package_dep_closure - owner_dep_closure(f)
```

Aggregate:

```text
median_extra_deps
p90_extra_deps
max_extra_deps
```

### Test Amplification

This is a deeper, more expensive metric.

For every source-like file:

```text
owner_tests(f) =
    rdeps(//..., owner_rules(f)) intersect kind(".*_test rule", //...)

package_tests(f) =
    rdeps(//..., //pkg:*) intersect kind(".*_test rule", //...)

test_amplification(f) =
    len(package_tests) / max(1, len(owner_tests))
```

This should initially be gated behind `--deep`, because it can require many
reverse dependency queries.

### Cohesion Score

The final user-facing score should combine multiple signals:

```text
cohesion_score =
    weighted dependency overlap
  + weighted source ownership connectedness
  + weighted internal rule graph connectedness
  + optional historical co-change overlap
```

Low cohesion plus high amplification should produce the strongest warning.

The first implementation does not need a sophisticated score. It can rank by:

```text
p90_dependency_amplification * log(1 + p90_extra_deps)
```

This favors packages with both high ratio and meaningful absolute waste.

## Data Model

The audit needs a package model:

```go
type PackageAudit struct {
    Package string
    Rules   []RuleInfo
    Files   []FileInfo
    Metrics PackageMetrics
}

type RuleInfo struct {
    Label string
    Kind  string
    Srcs  []string
    Hdrs  []string
    Data  []string
    Deps  []string
}

type FileInfo struct {
    Path       string
    OwnerRules []string
    Metrics    FileMetrics
}
```

Source-like attributes should include at least:

- `srcs`
- `hdrs`
- `data`
- `resources`
- language-specific source attributes when exposed by Bazel query output

`deps`-like attributes should include at least:

- `deps`
- `runtime_deps`
- `exports`
- `implementation_deps`, where present

The implementation should keep the attribute lists explicit and conservative.
It is better to miss a special-case attribute in the first version than to
produce confusing results by treating every label-valued attribute as source
ownership.

## Bazel Queries

### Package Discovery

For an explicit pattern:

```bash
bazel query 'buildfiles(//foo/...)'
```

or:

```bash
bazel query '//foo/...'
```

Then derive package names from labels.

For the default command, analyze all packages:

```bash
bazel query '//...'
```

The implementation should deduplicate package names before running per-package
analysis.

### Rule Metadata

Use structured query output rather than parsing BUILD files manually:

```bash
bazel query '//pkg:*' --output=xml
```

XML is acceptable because Go's standard library can parse it. If Bazel's proto
output is easier to consume later, the internal parser can be swapped behind the
same interface.

The parser should extract rule kind, label, and selected attributes.

### Dependency Closure

For the package-level dependency closure:

```bash
bazel query 'deps(//pkg:*)'
```

For owner-rule dependency closure:

```bash
bazel query 'deps(set(//pkg:rule_a //pkg:rule_b))'
```

For performance, avoid querying per file when files share the same owner-rule
set. Compute a stable owner set key and cache the closure per owner set.

### Test Reverse Dependencies

For `--deep`:

```bash
bazel query \
  'rdeps(//..., //pkg:*) intersect kind(".*_test rule", //...)' \
  --keep_going --nohost_deps --noimplicit_deps
```

For owner rules:

```bash
bazel query \
  'rdeps(//..., set(//pkg:rule_a //pkg:rule_b)) intersect kind(".*_test rule", //...)' \
  --keep_going --nohost_deps --noimplicit_deps
```

As with dependency closure, cache by owner-rule set.

## Output

Human output should be ranked by severity:

```text
Package cohesion audit

High priority

//services/payment
  rules: 37
  source files: 214
  p90 dependency amplification: 18.4x
  p90 extra deps: 227
  worst file: services/payment/reconcile.go
    owner rules: //services/payment:reconcile_lib
    owner deps: 14
    package deps: 258
    extra deps: 244

  Candidate clusters:
    payment_api: api_lib, api_test
    ledger: ledger_lib, ledger_test
    reconciliation: reconcile_lib, reconcile_test
```

JSON output should include enough detail for an external LLM-based advisor:

```json
{
  "packages": [
    {
      "package": "//services/payment",
      "rules": 37,
      "sourceFiles": 214,
      "metrics": {
        "p90DependencyAmplification": 18.4,
        "p90ExtraDeps": 227
      },
      "worstFiles": [
        {
          "path": "services/payment/reconcile.go",
          "ownerRules": ["//services/payment:reconcile_lib"],
          "ownerDeps": 14,
          "packageDeps": 258,
          "extraDeps": 244,
          "dependencyAmplification": 18.4
        }
      ],
      "clusters": []
    }
  ]
}
```

JSON keys use camelCase to match the repository's existing lint conventions
(see `.golangci.yml`). Snake_case keys in early drafts of this design were
adjusted before the first implementation; consumers should expect camelCase.

## Clustering and Suggestions

The first implementation can omit clustering and still be useful. Once metrics
exist, add rule clustering to suggest split boundaries.

Build a rule graph where edges indicate likely cohesion:

- one rule directly depends on another rule in the same package
- two rules share source files
- two rules share a large fraction of dependency closure
- a test rule depends on a library rule
- optional: files owned by both rules frequently co-change in git history

Connected components or thresholded community detection are enough. The output
should be advisory:

```text
Candidate split:
  //pkg/api
  //pkg/ledger
  //pkg/reconciliation
```

The tool should explain the evidence instead of pretending the split is
definitive.

## LLM Advisor

An LLM-based skill can consume `--format=json` output and produce a refactoring
plan:

- name conceptual package boundaries
- identify likely shared libraries or test utilities
- propose a migration order
- warn about files with ambiguous ownership

The LLM should not be required to compute the metrics. Deterministic analysis
keeps CI output reproducible and makes results easy to compare over time.

## Caching

Audit queries can be slower than the normal affected-test path. Cache at these
levels:

- package metadata from `bazel query //pkg:* --output=xml`
- package dependency closure from `deps(//pkg:*)`
- owner-set dependency closure from `deps(set(...))`
- optional test reverse dependencies for `--deep`

The existing BUILD and `.bzl` hash cache key can be reused. If history analysis
is enabled, include the history parameters in the cache key:

```text
history_depth
base_ref
```

## Implementation Plan

### Phase 1: Dependency Amplification

- Add `audit-packages` subcommand or flag-dispatched command mode.
- Add package metadata parser using `bazel query --output=xml`.
- Build file-to-owner-rules mapping.
- Query package dependency closure.
- Query owner-set dependency closures.
- Compute dependency amplification and extra dependency counts.
- Emit text and JSON output.

This phase is enough to validate the core metric.

### Phase 2: Better Ranking and Filtering

- Add package/rule count thresholds to avoid noisy tiny packages.
- Add `--fail-on-score`.
- Add config support for ignores and thresholds.
- Add timing instrumentation for audit stages.

### Phase 3: Deep Test Amplification

- Add `--deep`.
- Query package-level and owner-set reverse test dependencies.
- Report test amplification alongside dependency amplification.
- Cache reverse dependency results.

### Phase 4: Clustering

- Build same-package rule cohesion graph.
- Emit candidate clusters.
- Use clusters to make split suggestions more actionable.

### Phase 5: History and LLM Advisor

- Add optional git co-change analysis.
- Add JSON schema documentation.
- Add an LLM skill or prompt that turns JSON output into a refactoring plan.

## Risks and Mitigations

### Query Cost

Per-file queries would be too expensive. Query by unique owner-rule set instead.
Make test reverse dependency analysis opt-in and cache all query results.

### Ambiguous Ownership

Some files may be generated, exported, or referenced through macros. The audit
should report files with no owner or multiple owners separately and avoid using
them as strong evidence.

### Macro-Generated Rules

Using Bazel query output means the tool sees evaluated rules, not raw BUILD
syntax. That is good for correctness, but suggestions may not map one-to-one to
macro calls. The output should list labels and evidence, not prescribe exact
BUILD edits.

### False Positives

Some low-overlap packages are intentionally grouped for ownership or release
reasons. The command should be advisory by default and should provide enough
evidence for humans to dismiss acceptable cases.

## Success Criteria

- The audit identifies packages where one-file changes inherit large unrelated
  dependency closures.
- Results are deterministic without an LLM.
- The first implementation is useful with dependency amplification alone.
- Deep test amplification is available but optional.
- JSON output is rich enough for future LLM-assisted refactoring suggestions.
