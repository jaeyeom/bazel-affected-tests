// Package audit computes per-package cohesion metrics by comparing the
// dependency closure a file inherits via package-level granularity
// (//pkg:*) with the closure it would inherit from only its owner rules.
//
// The core metric, dependency_amplification(f), is the ratio of those two
// closure sizes. Packages with consistently high amplification are good
// candidates for splitting.
package audit

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/jaeyeom/bazel-affected-tests/internal/query"
)

// Querier is the subset of internal/query that the audit depends on.
type Querier interface {
	QueryRules(pattern string) ([]query.Rule, error)
	QueryDeps(targets []string) ([]string, error)
}

// PackageAudit is the audit result for a single Bazel package.
type PackageAudit struct {
	Package         string
	RuleCount       int
	SourceFileCount int
	Rules           []RuleInfo
	Files           []FileInfo
	Metrics         PackageMetrics
}

// RuleInfo summarizes one rule found in a package.
type RuleInfo struct {
	Label string
	Kind  string
}

// FileInfo holds a source file's owner rules and per-file metrics.
type FileInfo struct {
	Path       string
	OwnerRules []string
	Metrics    FileMetrics
}

// FileMetrics is the per-file dependency amplification result.
type FileMetrics struct {
	OwnerDepCount           int
	PackageDepCount         int
	ExtraDepCount           int
	DependencyAmplification float64
}

// PackageMetrics aggregates FileMetrics across all source files in a package.
type PackageMetrics struct {
	PackageDepCount int

	MedianDependencyAmplification float64
	P90DependencyAmplification    float64
	MaxDependencyAmplification    float64

	MedianExtraDeps int
	P90ExtraDeps    int
	MaxExtraDeps    int
}

// Auditor runs the audit for one or more packages and caches dependency
// closures keyed by owner-rule set so repeated owner sets aren't requeried.
type Auditor struct {
	q       Querier
	closure map[string][]string
}

// NewAuditor returns an Auditor backed by the given Querier.
func NewAuditor(q Querier) *Auditor {
	return &Auditor{q: q, closure: map[string][]string{}}
}

// AuditPackage produces the audit for a single Bazel package label. pkg may
// be a normal package like "//pkg/foo" or the root package "//". Packages
// with no rules return a result with zeroed metrics rather than an error.
func (a *Auditor) AuditPackage(pkg string) (*PackageAudit, error) {
	rules, err := a.q.QueryRules(pkg + ":*")
	if err != nil {
		return nil, fmt.Errorf("listing rules for %s: %w", pkg, err)
	}
	result := &PackageAudit{Package: pkg, RuleCount: len(rules)}
	if len(rules) == 0 {
		return result, nil
	}

	pkgDeps, err := a.q.QueryDeps([]string{pkg + ":*"})
	if err != nil {
		return nil, fmt.Errorf("dep closure for %s: %w", pkg, err)
	}

	for _, r := range rules {
		result.Rules = append(result.Rules, RuleInfo{Label: r.Label, Kind: r.Kind})
	}
	sort.Slice(result.Rules, func(i, j int) bool {
		return result.Rules[i].Label < result.Rules[j].Label
	})

	fileOwners := buildFileOwners(rules, pkg)
	result.SourceFileCount = len(fileOwners)
	if len(fileOwners) == 0 {
		result.Metrics = PackageMetrics{PackageDepCount: len(pkgDeps)}
		return result, nil
	}

	files, err := a.computeFileMetrics(fileOwners, pkgDeps)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	result.Files = files
	result.Metrics = aggregate(files, len(pkgDeps))
	return result, nil
}

// AuditPackages runs AuditPackage for each package, returning one result per
// input package. Errors short-circuit the run.
func (a *Auditor) AuditPackages(packages []string) ([]*PackageAudit, error) {
	results := make([]*PackageAudit, 0, len(packages))
	for _, p := range packages {
		audit, err := a.AuditPackage(p)
		if err != nil {
			return nil, err
		}
		results = append(results, audit)
	}
	return results, nil
}

func (a *Auditor) computeFileMetrics(fileOwners map[string][]string, pkgDeps []string) ([]FileInfo, error) {
	pkgDepSet := stringSet(pkgDeps)
	files := make([]FileInfo, 0, len(fileOwners))
	for path, owners := range fileOwners {
		owners = uniqueSorted(owners)
		ownerDeps, err := a.ownerSetDeps(owners)
		if err != nil {
			return nil, fmt.Errorf("dep closure for owners of %s: %w", path, err)
		}
		ownerSet := stringSet(ownerDeps)
		extra := 0
		for d := range pkgDepSet {
			if !ownerSet[d] {
				extra++
			}
		}
		ownerCount := len(ownerDeps)
		amp := float64(len(pkgDeps)) / math.Max(1, float64(ownerCount))
		files = append(files, FileInfo{
			Path:       path,
			OwnerRules: owners,
			Metrics: FileMetrics{
				OwnerDepCount:           ownerCount,
				PackageDepCount:         len(pkgDeps),
				ExtraDepCount:           extra,
				DependencyAmplification: amp,
			},
		})
	}
	return files, nil
}

// ownerSetDeps returns the dependency closure of the given owner labels,
// caching results by a stable key derived from the owner set so repeated
// requests across files share one query.
func (a *Auditor) ownerSetDeps(owners []string) ([]string, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	key := strings.Join(owners, "|") // owners is sorted+unique by caller
	if cached, ok := a.closure[key]; ok {
		return cached, nil
	}
	deps, err := a.q.QueryDeps(owners)
	if err != nil {
		return nil, fmt.Errorf("querying deps for owner set: %w", err)
	}
	a.closure[key] = deps
	return deps, nil
}

func aggregate(files []FileInfo, pkgDepCount int) PackageMetrics {
	amps := make([]float64, len(files))
	extras := make([]int, len(files))
	for i, f := range files {
		amps[i] = f.Metrics.DependencyAmplification
		extras[i] = f.Metrics.ExtraDepCount
	}
	return PackageMetrics{
		PackageDepCount:               pkgDepCount,
		MedianDependencyAmplification: percentileFloat(amps, 50),
		P90DependencyAmplification:    percentileFloat(amps, 90),
		MaxDependencyAmplification:    sliceMax(amps),
		MedianExtraDeps:               percentileInt(extras, 50),
		P90ExtraDeps:                  percentileInt(extras, 90),
		MaxExtraDeps:                  sliceMax(extras),
	}
}

// buildFileOwners walks each rule's source-like attributes, mapping every
// source label that belongs to pkg back to a workspace-relative path. Source
// labels in other packages are skipped — they're owned by a different package
// audit.
func buildFileOwners(rules []query.Rule, pkg string) map[string][]string {
	owners := make(map[string][]string)
	for _, r := range rules {
		for _, attr := range query.SourceAttrs {
			for _, lbl := range r.Sources[attr] {
				path := labelToPath(lbl, pkg)
				if path == "" {
					continue
				}
				owners[path] = append(owners[path], r.Label)
			}
		}
	}
	return owners
}

// labelToPath converts a Bazel label into a workspace-relative file path
// when the label refers to a file in pkg. Returns "" otherwise.
//
// Examples (pkg="//pkg/foo"):
//
//	"//pkg/foo:bar.go"        -> "pkg/foo/bar.go"
//	"//pkg/foo:sub/bar.go"    -> "pkg/foo/sub/bar.go"
//	"//other:foo.go"          -> ""
//	"//pkg/foo:lib"           -> "pkg/foo/lib" (caller filters non-source labels)
func labelToPath(label, pkg string) string {
	if !strings.HasPrefix(label, "//") {
		return ""
	}
	labelPkg, name, ok := strings.Cut(label[2:], ":")
	if !ok || name == "" {
		return ""
	}
	if labelPkg != strings.TrimPrefix(pkg, "//") {
		return ""
	}
	if labelPkg == "" {
		return name
	}
	return labelPkg + "/" + name
}

func stringSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

func uniqueSorted(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	set := stringSet(s)
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// percentileFloat returns the value at the p-th percentile (0-100) using the
// nearest-rank method. Empty input returns 0.
func percentileFloat(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	idx = max(idx, 0)
	idx = min(idx, len(sorted)-1)
	return sorted[idx]
}

func percentileInt(values []int, p float64) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	idx = max(idx, 0)
	idx = min(idx, len(sorted)-1)
	return sorted[idx]
}

// sliceMax returns the maximum of values, or the zero value when the slice
// is empty. slices.Max panics on empty input; the audit would rather report
// "no signal" than crash on empty packages.
func sliceMax[T cmp.Ordered](values []T) T {
	var zero T
	if len(values) == 0 {
		return zero
	}
	return slices.Max(values)
}
