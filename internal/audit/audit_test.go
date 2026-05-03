package audit

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jaeyeom/bazel-affected-tests/internal/query"
)

// fakeQuerier records calls and returns canned results. depsByKey is keyed by
// the sorted-and-joined target list so call ordering doesn't matter.
type fakeQuerier struct {
	rules     map[string][]query.Rule
	depsByKey map[string][]string
	depCalls  []string
	ruleCalls []string
	depErr    error
	ruleErr   error
}

func (f *fakeQuerier) QueryRules(pattern string) ([]query.Rule, error) {
	f.ruleCalls = append(f.ruleCalls, pattern)
	if f.ruleErr != nil {
		return nil, f.ruleErr
	}
	return f.rules[pattern], nil
}

func (f *fakeQuerier) QueryDeps(targets []string) ([]string, error) {
	key := depKey(targets)
	f.depCalls = append(f.depCalls, key)
	if f.depErr != nil {
		return nil, f.depErr
	}
	return f.depsByKey[key], nil
}

func depKey(targets []string) string {
	sorted := append([]string(nil), targets...)
	sort.Strings(sorted)
	return strings.Join(sorted, "|")
}

func TestLabelToPath(t *testing.T) {
	cases := []struct {
		name  string
		label string
		pkg   string
		want  string
	}{
		{"basic file in pkg", "//pkg/foo:bar.go", "//pkg/foo", "pkg/foo/bar.go"},
		{"file in subdir of pkg", "//pkg/foo:sub/bar.go", "//pkg/foo", "pkg/foo/sub/bar.go"},
		{"file in root pkg", "//:root.go", "//", "root.go"},
		{"foreign pkg returns empty", "//other:foo.go", "//pkg/foo", ""},
		{"no colon returns empty", "//pkg/foo", "//pkg/foo", ""},
		{"empty name returns empty", "//pkg/foo:", "//pkg/foo", ""},
		{"missing slashes returns empty", "pkg/foo:bar", "//pkg/foo", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := labelToPath(tc.label, tc.pkg)
			if got != tc.want {
				t.Errorf("labelToPath(%q, %q) = %q, want %q", tc.label, tc.pkg, got, tc.want)
			}
		})
	}
}

func TestPercentileFloat(t *testing.T) {
	cases := []struct {
		name   string
		values []float64
		p      float64
		want   float64
	}{
		{"empty", nil, 50, 0},
		{"single", []float64{2.5}, 50, 2.5},
		{"single p90", []float64{2.5}, 90, 2.5},
		{"sorted p50 of 4", []float64{1.0, 2.0, 3.0, 4.0}, 50, 2.0},
		{"sorted p90 of 4", []float64{1.0, 2.0, 3.0, 4.0}, 90, 4.0},
		{"unsorted input", []float64{4.0, 1.0, 3.0, 2.0}, 90, 4.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := percentileFloat(tc.values, tc.p)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("percentileFloat(%v, %v) = %v, want %v", tc.values, tc.p, got, tc.want)
			}
		})
	}
}

func TestPercentileInt(t *testing.T) {
	if got := percentileInt(nil, 90); got != 0 {
		t.Errorf("empty input should be 0, got %d", got)
	}
	if got := percentileInt([]int{1, 2, 3, 4}, 50); got != 2 {
		t.Errorf("p50 of [1,2,3,4] = %d, want 2", got)
	}
	if got := percentileInt([]int{1, 2, 3, 4}, 90); got != 4 {
		t.Errorf("p90 of [1,2,3,4] = %d, want 4", got)
	}
}

func TestAuditPackage_BasicAmplification(t *testing.T) {
	// Three rules in //pkg/foo:
	//   lib_a (srcs=a.go, common.go; deps=dep1, dep2)
	//   lib_b (srcs=b.go;             deps=dep3)
	//   lib_a_test (srcs=a_test.go;   deps=lib_a, dep_test)
	rules := []query.Rule{
		{
			Kind:    "go_library",
			Label:   "//pkg/foo:lib_a",
			Sources: map[string][]string{"srcs": {"//pkg/foo:a.go", "//pkg/foo:common.go"}},
			Deps:    map[string][]string{"deps": {"//dep1", "//dep2"}},
		},
		{
			Kind:    "go_library",
			Label:   "//pkg/foo:lib_b",
			Sources: map[string][]string{"srcs": {"//pkg/foo:b.go"}},
			Deps:    map[string][]string{"deps": {"//dep3"}},
		},
		{
			Kind:    "go_test",
			Label:   "//pkg/foo:lib_a_test",
			Sources: map[string][]string{"srcs": {"//pkg/foo:a_test.go"}},
			Deps:    map[string][]string{"deps": {"//pkg/foo:lib_a", "//dep_test"}},
		},
	}

	pkgDeps := []string{
		"//pkg/foo:lib_a", "//pkg/foo:lib_b", "//pkg/foo:lib_a_test",
		"//dep1", "//dep2", "//dep3", "//dep_test",
	}
	libADeps := []string{"//pkg/foo:lib_a", "//dep1", "//dep2"}
	libBDeps := []string{"//pkg/foo:lib_b", "//dep3"}
	libATestDeps := []string{"//pkg/foo:lib_a_test", "//pkg/foo:lib_a", "//dep1", "//dep2", "//dep_test"}

	q := &fakeQuerier{
		rules: map[string][]query.Rule{"//pkg/foo:*": rules},
		depsByKey: map[string][]string{
			depKey([]string{"//pkg/foo:*"}):          pkgDeps,
			depKey([]string{"//pkg/foo:lib_a"}):      libADeps,
			depKey([]string{"//pkg/foo:lib_b"}):      libBDeps,
			depKey([]string{"//pkg/foo:lib_a_test"}): libATestDeps,
		},
	}

	audit, err := NewAuditor(q).AuditPackage("//pkg/foo")
	if err != nil {
		t.Fatalf("AuditPackage failed: %v", err)
	}

	if audit.RuleCount != 3 {
		t.Errorf("RuleCount = %d, want 3", audit.RuleCount)
	}
	if audit.SourceFileCount != 4 {
		t.Errorf("SourceFileCount = %d, want 4", audit.SourceFileCount)
	}

	wantPaths := []string{"pkg/foo/a.go", "pkg/foo/a_test.go", "pkg/foo/b.go", "pkg/foo/common.go"}
	gotPaths := make([]string, len(audit.Files))
	for i, f := range audit.Files {
		gotPaths[i] = f.Path
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Errorf("file paths = %v, want %v", gotPaths, wantPaths)
	}

	byPath := make(map[string]FileInfo, len(audit.Files))
	for _, f := range audit.Files {
		byPath[f.Path] = f
	}

	checkFile := func(path string, wantOwnerCount, wantExtra int, wantAmp float64) {
		t.Helper()
		f, ok := byPath[path]
		if !ok {
			t.Fatalf("missing file %q", path)
		}
		if f.Metrics.OwnerDepCount != wantOwnerCount {
			t.Errorf("%s OwnerDepCount = %d, want %d", path, f.Metrics.OwnerDepCount, wantOwnerCount)
		}
		if f.Metrics.PackageDepCount != 7 {
			t.Errorf("%s PackageDepCount = %d, want 7", path, f.Metrics.PackageDepCount)
		}
		if f.Metrics.ExtraDepCount != wantExtra {
			t.Errorf("%s ExtraDepCount = %d, want %d", path, f.Metrics.ExtraDepCount, wantExtra)
		}
		if math.Abs(f.Metrics.DependencyAmplification-wantAmp) > 1e-6 {
			t.Errorf("%s amp = %v, want %v", path, f.Metrics.DependencyAmplification, wantAmp)
		}
	}

	// pkg deps = 7
	// a.go owned by lib_a (closure 3): extra=4, amp=7/3
	checkFile("pkg/foo/a.go", 3, 4, 7.0/3.0)
	// common.go also owned by lib_a — same metrics, same cached closure
	checkFile("pkg/foo/common.go", 3, 4, 7.0/3.0)
	// b.go owned by lib_b (closure 2): extra=5, amp=7/2
	checkFile("pkg/foo/b.go", 2, 5, 7.0/2.0)
	// a_test.go owned by lib_a_test (closure 5): extra=2, amp=7/5
	checkFile("pkg/foo/a_test.go", 5, 2, 7.0/5.0)

	// Aggregate p50/p90/max — see comment above for hand-computed values.
	if audit.Metrics.MedianDependencyAmplification != 7.0/3.0 {
		t.Errorf("median amp = %v, want %v", audit.Metrics.MedianDependencyAmplification, 7.0/3.0)
	}
	if audit.Metrics.P90DependencyAmplification != 7.0/2.0 {
		t.Errorf("p90 amp = %v, want %v", audit.Metrics.P90DependencyAmplification, 7.0/2.0)
	}
	if audit.Metrics.MaxDependencyAmplification != 7.0/2.0 {
		t.Errorf("max amp = %v, want %v", audit.Metrics.MaxDependencyAmplification, 7.0/2.0)
	}
	if audit.Metrics.MedianExtraDeps != 4 {
		t.Errorf("median extra = %d, want 4", audit.Metrics.MedianExtraDeps)
	}
	if audit.Metrics.P90ExtraDeps != 5 {
		t.Errorf("p90 extra = %d, want 5", audit.Metrics.P90ExtraDeps)
	}
	if audit.Metrics.MaxExtraDeps != 5 {
		t.Errorf("max extra = %d, want 5", audit.Metrics.MaxExtraDeps)
	}
}

func TestAuditPackage_OwnerSetCachedAcrossFiles(t *testing.T) {
	// lib_a owns two files; lib_a's deps should be queried only once.
	rules := []query.Rule{
		{
			Kind:    "go_library",
			Label:   "//pkg/foo:lib_a",
			Sources: map[string][]string{"srcs": {"//pkg/foo:a.go", "//pkg/foo:common.go"}},
		},
	}
	q := &fakeQuerier{
		rules: map[string][]query.Rule{"//pkg/foo:*": rules},
		depsByKey: map[string][]string{
			depKey([]string{"//pkg/foo:*"}):     {"//pkg/foo:lib_a", "//dep1"},
			depKey([]string{"//pkg/foo:lib_a"}): {"//pkg/foo:lib_a", "//dep1"},
		},
	}

	if _, err := NewAuditor(q).AuditPackage("//pkg/foo"); err != nil {
		t.Fatalf("AuditPackage failed: %v", err)
	}

	ownerCalls := 0
	for _, k := range q.depCalls {
		if k == depKey([]string{"//pkg/foo:lib_a"}) {
			ownerCalls++
		}
	}
	if ownerCalls != 1 {
		t.Errorf("expected lib_a closure to be queried once, got %d calls", ownerCalls)
	}
}

func TestAuditPackage_FileWithMultipleOwnersUsesUnion(t *testing.T) {
	// shared.go appears in both lib_a and lib_b, so its owner-set is the union.
	rules := []query.Rule{
		{
			Kind:    "go_library",
			Label:   "//pkg/foo:lib_a",
			Sources: map[string][]string{"srcs": {"//pkg/foo:shared.go"}},
		},
		{
			Kind:    "go_library",
			Label:   "//pkg/foo:lib_b",
			Sources: map[string][]string{"srcs": {"//pkg/foo:shared.go"}},
		},
	}
	q := &fakeQuerier{
		rules: map[string][]query.Rule{"//pkg/foo:*": rules},
		depsByKey: map[string][]string{
			depKey([]string{"//pkg/foo:*"}): {"//pkg/foo:lib_a", "//pkg/foo:lib_b", "//dep1"},
			// Union closure for both libs together.
			depKey([]string{"//pkg/foo:lib_a", "//pkg/foo:lib_b"}): {"//pkg/foo:lib_a", "//pkg/foo:lib_b", "//dep1"},
		},
	}

	audit, err := NewAuditor(q).AuditPackage("//pkg/foo")
	if err != nil {
		t.Fatalf("AuditPackage failed: %v", err)
	}
	if len(audit.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(audit.Files))
	}
	f := audit.Files[0]
	wantOwners := []string{"//pkg/foo:lib_a", "//pkg/foo:lib_b"}
	if !reflect.DeepEqual(f.OwnerRules, wantOwners) {
		t.Errorf("OwnerRules = %v, want %v", f.OwnerRules, wantOwners)
	}
	// Closure size matches package, so amp should be 1.0 and extra 0.
	if f.Metrics.DependencyAmplification != 1.0 {
		t.Errorf("amp = %v, want 1.0", f.Metrics.DependencyAmplification)
	}
	if f.Metrics.ExtraDepCount != 0 {
		t.Errorf("extra = %d, want 0", f.Metrics.ExtraDepCount)
	}
}

func TestAuditPackage_ForeignSourceLabelsSkipped(t *testing.T) {
	// A rule's srcs include a label from another package; that file must not
	// be attributed to //pkg/foo.
	rules := []query.Rule{
		{
			Kind:    "go_library",
			Label:   "//pkg/foo:lib",
			Sources: map[string][]string{"srcs": {"//pkg/foo:a.go", "//other:foreign.go"}},
		},
	}
	q := &fakeQuerier{
		rules: map[string][]query.Rule{"//pkg/foo:*": rules},
		depsByKey: map[string][]string{
			depKey([]string{"//pkg/foo:*"}):   {"//pkg/foo:lib", "//other:foreign.go"},
			depKey([]string{"//pkg/foo:lib"}): {"//pkg/foo:lib", "//other:foreign.go"},
		},
	}
	audit, err := NewAuditor(q).AuditPackage("//pkg/foo")
	if err != nil {
		t.Fatalf("AuditPackage failed: %v", err)
	}
	if audit.SourceFileCount != 1 {
		t.Errorf("SourceFileCount = %d, want 1", audit.SourceFileCount)
	}
	if len(audit.Files) != 1 || audit.Files[0].Path != "pkg/foo/a.go" {
		t.Errorf("expected single file pkg/foo/a.go, got %+v", audit.Files)
	}
}

func TestAuditPackage_EmptyPackageReturnsZeroMetrics(t *testing.T) {
	q := &fakeQuerier{
		rules: map[string][]query.Rule{},
	}
	audit, err := NewAuditor(q).AuditPackage("//empty")
	if err != nil {
		t.Fatalf("AuditPackage failed: %v", err)
	}
	if audit.RuleCount != 0 || audit.SourceFileCount != 0 {
		t.Errorf("expected zeroed counts, got %+v", audit)
	}
	if len(audit.Files) != 0 {
		t.Errorf("expected no files, got %d", len(audit.Files))
	}
	// No QueryDeps call when no rules.
	if len(q.depCalls) != 0 {
		t.Errorf("expected no QueryDeps calls for empty pkg, got %d", len(q.depCalls))
	}
}

func TestAuditPackage_RuleQueryError(t *testing.T) {
	q := &fakeQuerier{ruleErr: errors.New("boom")}
	_, err := NewAuditor(q).AuditPackage("//pkg/foo")
	if err == nil {
		t.Fatal("expected error from rule query")
	}
	if !strings.Contains(err.Error(), "listing rules for //pkg/foo") {
		t.Errorf("error should be wrapped with package context, got: %v", err)
	}
}

func TestAuditPackage_DepQueryError(t *testing.T) {
	q := &fakeQuerier{
		rules: map[string][]query.Rule{"//pkg/foo:*": {
			{Label: "//pkg/foo:lib", Sources: map[string][]string{"srcs": {"//pkg/foo:a.go"}}},
		}},
		depErr: errors.New("boom"),
	}
	_, err := NewAuditor(q).AuditPackage("//pkg/foo")
	if err == nil {
		t.Fatal("expected error from dep query")
	}
	if !strings.Contains(err.Error(), "dep closure for //pkg/foo") {
		t.Errorf("error should mention dep closure, got: %v", err)
	}
}

func TestAuditPackages_RunsAll(t *testing.T) {
	q := &fakeQuerier{
		rules: map[string][]query.Rule{
			"//a:*": {{Label: "//a:lib", Sources: map[string][]string{"srcs": {"//a:x.go"}}}},
			"//b:*": {{Label: "//b:lib", Sources: map[string][]string{"srcs": {"//b:y.go"}}}},
		},
		depsByKey: map[string][]string{
			depKey([]string{"//a:*"}):   {"//a:lib"},
			depKey([]string{"//a:lib"}): {"//a:lib"},
			depKey([]string{"//b:*"}):   {"//b:lib"},
			depKey([]string{"//b:lib"}): {"//b:lib"},
		},
	}
	audits, err := NewAuditor(q).AuditPackages([]string{"//a", "//b"})
	if err != nil {
		t.Fatalf("AuditPackages failed: %v", err)
	}
	if len(audits) != 2 {
		t.Errorf("expected 2 audits, got %d", len(audits))
	}
	if audits[0].Package != "//a" || audits[1].Package != "//b" {
		t.Errorf("results out of order: %+v", audits)
	}
}
