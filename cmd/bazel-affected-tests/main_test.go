package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/jaeyeom/bazel-affected-tests/internal/cache"
	"github.com/jaeyeom/bazel-affected-tests/internal/config"
	"github.com/jaeyeom/bazel-affected-tests/internal/query"
	executor "github.com/jaeyeom/go-cmdexec"
)

func TestMergeTargets(t *testing.T) {
	tests := []struct {
		name          string
		tests         []string
		configTargets []string
		want          []string
	}{
		{"empty", nil, nil, []string{}},
		{"tests only", []string{"//a:t1", "//b:t2"}, nil, []string{"//a:t1", "//b:t2"}},
		{"config only", nil, []string{"//c:t3"}, []string{"//c:t3"}},
		{"deduplicates", []string{"//a:t1", "//b:t2"}, []string{"//a:t1", "//c:t3"}, []string{"//a:t1", "//b:t2", "//c:t3"}},
		{"sorted", []string{"//z:t", "//a:t"}, nil, []string{"//a:t", "//z:t"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeTargets(tt.tests, tt.configTargets)
			if len(got) == 0 && len(tt.want) == 0 {
				return // both empty
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeTargets() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunBazelTest(t *testing.T) {
	targets := []string{"//pkg/foo:test1", "//pkg/bar:test2"}

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "test", "//pkg/foo:test1", "//pkg/bar:test2").
		WillSucceed("", 0).
		Once().
		Build()

	exitCode, err := runBazelTest(mockExec, targets)
	if err != nil {
		t.Fatalf("runBazelTest() error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}

	if err := mockExec.AssertExpectationsMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}

func TestRunBazelTest_Failure(t *testing.T) {
	targets := []string{"//pkg/foo:failing_test"}

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "test", "//pkg/foo:failing_test").
		WillSucceed("", 3). // bazel test returns non-zero for test failures
		Once().
		Build()

	exitCode, err := runBazelTest(mockExec, targets)
	if err != nil {
		t.Fatalf("runBazelTest() error: %v", err)
	}
	if exitCode != 3 {
		t.Errorf("exit code = %d, want 3", exitCode)
	}

	if err := mockExec.AssertExpectationsMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}

func sorted(xs []string) []string {
	out := slices.Clone(xs)
	slices.Sort(out)
	return out
}

func TestCountSourceFlags(t *testing.T) {
	tests := []struct {
		name string
		cfg  cliConfig
		want int
	}{
		{"no flags", cliConfig{}, 0},
		{"staged only", cliConfig{staged: true}, 1},
		{"head only", cliConfig{head: true}, 1},
		{"base only", cliConfig{base: "main"}, 1},
		{"files-from only", cliConfig{filesFrom: "list.txt"}, 1},
		{"staged and head", cliConfig{staged: true, head: true}, 2},
		{"staged and base", cliConfig{staged: true, base: "main"}, 2},
		{"all four", cliConfig{staged: true, head: true, base: "main", filesFrom: "-"}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countSourceFlags(tt.cfg); got != tt.want {
				t.Errorf("countSourceFlags() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetPackageTests_UsesCacheWhenAvailable(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir)
	cacheKey := "k1"
	pkg := "//pkg/foo"
	cached := []string{"//pkg/foo:cached_test"}
	if err := c.Set(cacheKey, pkg, cached); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	mockExec := executor.NewMockExecutor()
	q := query.NewBazelQuerierWithExecutor(mockExec)

	got, err := getPackageTests(pkg, q, c, cacheKey, false)
	if err != nil {
		t.Fatalf("getPackageTests() error: %v", err)
	}
	if !reflect.DeepEqual(got, cached) {
		t.Fatalf("getPackageTests() = %v, want %v", got, cached)
	}

	history := mockExec.GetCallHistory()
	if len(history) != 0 {
		t.Fatalf("expected no bazel calls on cache hit, got %d", len(history))
	}
}

func TestGetPackageTests_CacheMissQueriesAndStores(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir)
	cacheKey := "k1"
	pkg := "//pkg/foo"

	// FindAffectedTests internally makes 3 bazel queries per package:
	// 1. kind('.*_test rule', PKG:*)         -- same-package tests
	// 2. kind('.*_test rule', PKG/...)       -- sub-package tests
	// 3. rdeps(//..., PKG:*) intersect ...   -- reverse-dep tests
	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "query", "kind('.*_test rule', //pkg/foo:*)").
		WillSucceed("//pkg/foo:unit_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "kind('.*_test rule', //pkg/foo/...)").
		WillSucceed("//pkg/foo:unit_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--keep_going", "--nohost_deps", "--noimplicit_deps", "rdeps(//..., //pkg/foo:*) intersect kind('.*_test rule', //...)").
		WillSucceed("//dep:dep_test", 0).
		Once().
		Build()
	q := query.NewBazelQuerierWithExecutor(mockExec)

	gotRaw, err := getPackageTests(pkg, q, c, cacheKey, false)
	if err != nil {
		t.Fatalf("getPackageTests() error: %v", err)
	}
	got := sorted(gotRaw)
	want := sorted([]string{"//pkg/foo:unit_test", "//dep:dep_test"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("getPackageTests() = %v, want %v", got, want)
	}

	cached, found := c.Get(cacheKey, pkg)
	if !found {
		t.Error("expected cache entry to be stored")
	} else if !reflect.DeepEqual(sorted(cached), want) {
		t.Errorf("cached tests = %v, want %v", cached, want)
	}

	if err := mockExec.AssertExpectationsMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}

func TestGetPackageTests_NoCacheFlagBypassesReadAndWrite(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir)
	cacheKey := "k1"
	pkg := "//pkg/foo"
	if err := c.Set(cacheKey, pkg, []string{"//pkg/foo:old_cached"}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "query", "kind('.*_test rule', //pkg/foo:*)").
		WillSucceed("//pkg/foo:new_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "kind('.*_test rule', //pkg/foo/...)").
		WillSucceed("//pkg/foo:new_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--keep_going", "--nohost_deps", "--noimplicit_deps", "rdeps(//..., //pkg/foo:*) intersect kind('.*_test rule', //...)").
		WillSucceed("", 0).
		Once().
		Build()
	q := query.NewBazelQuerierWithExecutor(mockExec)

	got, err := getPackageTests(pkg, q, c, cacheKey, true)
	if err != nil {
		t.Fatalf("getPackageTests() error: %v", err)
	}
	want := []string{"//pkg/foo:new_test"}
	if !reflect.DeepEqual(sorted(got), sorted(want)) {
		t.Fatalf("getPackageTests() = %v, want %v", got, want)
	}

	cached, found := c.Get(cacheKey, pkg)
	if !found {
		t.Error("expected existing cache to remain")
	} else if !reflect.DeepEqual(cached, []string{"//pkg/foo:old_cached"}) {
		t.Errorf("cache should not be overwritten when noCache=true, got %v", cached)
	}

	if err := mockExec.AssertExpectationsMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}

func TestGetPackageTests_EmptyKeyBypassesReadAndWrite(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir)
	pkg := "//pkg/foo"

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "query", "kind('.*_test rule', //pkg/foo:*)").
		WillSucceed("//pkg/foo:new_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "kind('.*_test rule', //pkg/foo/...)").
		WillSucceed("//pkg/foo:new_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--keep_going", "--nohost_deps", "--noimplicit_deps", "rdeps(//..., //pkg/foo:*) intersect kind('.*_test rule', //...)").
		WillSucceed("", 0).
		Once().
		Build()
	q := query.NewBazelQuerierWithExecutor(mockExec)

	got, err := getPackageTests(pkg, q, c, "", false)
	if err != nil {
		t.Fatalf("getPackageTests() error: %v", err)
	}
	want := []string{"//pkg/foo:new_test"}
	if !reflect.DeepEqual(sorted(got), sorted(want)) {
		t.Fatalf("getPackageTests() = %v, want %v", got, want)
	}

	if _, found := c.Get("", pkg); found {
		t.Error("cache should not be written when cache key is empty")
	}

	if err := mockExec.AssertExpectationsMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}

func TestMergeTargets_ConfigOnlyNoPackages(t *testing.T) {
	// Simulates the config-only path: no Bazel packages found, but config
	// rules match changed files. Previously this returned nil because
	// resolveTargets returned early on len(packages) == 0.
	configTargets := []string{"//tools/format:format_test_Proto"}
	got := mergeTargets(nil, configTargets)
	want := []string{"//tools/format:format_test_Proto"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeTargets(nil, config) = %v, want %v", got, want)
	}
}

func TestCollectAllTests_DeduplicatesAcrossPackages(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir)
	cacheKey := "k1"
	if err := c.Set(cacheKey, "//pkg/foo", []string{"//pkg/foo:t1", "//shared:t"}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if err := c.Set(cacheKey, "//pkg/bar", []string{"//pkg/bar:t2", "//shared:t"}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	mockExec := executor.NewMockExecutor()
	q := query.NewBazelQuerierWithExecutor(mockExec)

	gotRaw, err := collectAllTests([]string{"//pkg/foo", "//pkg/bar"}, q, c, cacheKey, false)
	if err != nil {
		t.Fatalf("collectAllTests() error: %v", err)
	}
	got := sorted(gotRaw)
	want := sorted([]string{"//pkg/foo:t1", "//pkg/bar:t2", "//shared:t"})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collectAllTests() = %v, want %v", got, want)
	}
}

func TestReadFilesFrom_File(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "files.txt")
	content := "foo/bar.go\nbaz/qux.go\n\nignore_empty\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readFilesFrom(path)
	if err != nil {
		t.Fatalf("readFilesFrom() error: %v", err)
	}
	want := []string{"foo/bar.go", "baz/qux.go", "ignore_empty"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readFilesFrom() = %v, want %v", got, want)
	}
}

func TestReadFilesFrom_MissingFile(t *testing.T) {
	_, err := readFilesFrom("/nonexistent/path/to/file.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadFilesFrom_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readFilesFrom(path)
	if err != nil {
		t.Fatalf("readFilesFrom() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestFindPackages(t *testing.T) {
	// Create a temp repo root with BUILD files
	tmpDir := t.TempDir()
	pkgDir := filepath.Join(tmpDir, "pkg", "foo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "BUILD"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	got, unmapped := findPackages(tmpDir, []string{"pkg/foo/bar.go", "no/build/file.go"}, query.UnlimitedParentDepth)
	if len(got) != 1 {
		t.Fatalf("expected 1 package, got %d: %v", len(got), got)
	}
	if got[0] != "//pkg/foo" {
		t.Errorf("expected //pkg/foo, got %s", got[0])
	}
	if len(unmapped) != 1 || unmapped[0] != "no/build/file.go" {
		t.Errorf("expected unmapped [no/build/file.go], got %v", unmapped)
	}
}

func TestFindPackages_NoBuildFiles(t *testing.T) {
	tmpDir := t.TempDir()
	got, unmapped := findPackages(tmpDir, []string{"some/file.go"}, query.UnlimitedParentDepth)
	if len(got) != 0 {
		t.Errorf("expected no packages, got %v", got)
	}
	if len(unmapped) != 1 || unmapped[0] != "some/file.go" {
		t.Errorf("expected unmapped [some/file.go], got %v", unmapped)
	}
}

func TestFindPackages_RespectsMaxDepth(t *testing.T) {
	tmpDir := t.TempDir()
	pkgDir := filepath.Join(tmpDir, "pkg", "foo")
	deepDir := filepath.Join(pkgDir, "deep", "nested")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "BUILD"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	// depth=1 cannot reach pkg/foo from pkg/foo/deep/nested (2 hops up)
	packages, unmapped := findPackages(tmpDir, []string{"pkg/foo/deep/nested/file.go"}, 1)
	if len(packages) != 0 {
		t.Errorf("expected no packages at depth 1, got %v", packages)
	}
	if len(unmapped) != 1 {
		t.Errorf("expected 1 unmapped file at depth 1, got %v", unmapped)
	}

	// depth=2 does reach it
	packages, unmapped = findPackages(tmpDir, []string{"pkg/foo/deep/nested/file.go"}, 2)
	if len(packages) != 1 || packages[0] != "//pkg/foo" {
		t.Errorf("expected [//pkg/foo] at depth 2, got %v", packages)
	}
	if len(unmapped) != 0 {
		t.Errorf("expected no unmapped files at depth 2, got %v", unmapped)
	}
}

func TestResolveMaxParentDepth(t *testing.T) {
	intPtr := func(i int) *int { return &i }

	tests := []struct {
		name    string
		cfg     cliConfig
		repoCfg *config.Config
		want    int
	}{
		{"nothing set uses default", cliConfig{maxParentDepth: maxParentDepthUnset}, nil, config.DefaultMaxParentDepth},
		{"config overrides default", cliConfig{maxParentDepth: maxParentDepthUnset}, &config.Config{MaxParentDepth: intPtr(3)}, 3},
		{"flag overrides config", cliConfig{maxParentDepth: 0}, &config.Config{MaxParentDepth: intPtr(3)}, 0},
		{"flag -1 means unlimited", cliConfig{maxParentDepth: -1}, nil, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveMaxParentDepth(tt.cfg, tt.repoCfg); got != tt.want {
				t.Errorf("resolveMaxParentDepth() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolveStrict(t *testing.T) {
	tests := []struct {
		name    string
		cfg     cliConfig
		repoCfg *config.Config
		want    bool
	}{
		{"nothing set is false", cliConfig{}, nil, false},
		{"config true, flag not set", cliConfig{}, &config.Config{Strict: true}, true},
		{"flag false overrides config true", cliConfig{strict: false, strictSet: true}, &config.Config{Strict: true}, false},
		{"flag true overrides config false", cliConfig{strict: true, strictSet: true}, &config.Config{Strict: false}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveStrict(tt.cfg, tt.repoCfg); got != tt.want {
				t.Errorf("resolveStrict() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCacheKey_NoCacheReturnsEmpty(t *testing.T) {
	c := cache.NewCache(t.TempDir())
	key := getCacheKey(c, true, "/some/repo")
	if key != "" {
		t.Errorf("expected empty key with noCache=true, got %q", key)
	}
}

func TestHandleCacheClear(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir)

	// Write something to cache first
	if err := c.Set("key1", "//pkg", []string{"//pkg:test"}); err != nil {
		t.Fatal(err)
	}

	if err := handleCacheClear(c); err != nil {
		t.Fatalf("handleCacheClear() error: %v", err)
	}

	// Verify cache is cleared
	if _, found := c.Get("key1", "//pkg"); found {
		t.Error("expected cache to be cleared")
	}
}

func TestNewQuerier_NilConfig(t *testing.T) {
	q := newQuerier(nil)
	if q == nil {
		t.Fatal("newQuerier(nil) returned nil")
	}
}

func TestNewQuerier_WithConfig(t *testing.T) {
	falseVal := false
	cfg := &config.Config{
		EnableSubpackageQuery: &falseVal,
	}
	q := newQuerier(cfg)
	if q == nil {
		t.Fatal("newQuerier returned nil")
	}
}

func TestOutputOrRun_PrintsTargets(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	outputOrRun(false, []string{"//a:test", "//b:test"})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom() error: %v", err)
	}
	output := buf.String()

	if output != "//a:test\n//b:test\n" {
		t.Errorf("unexpected output: %q", output)
	}
}

func TestOutputOrRun_EmptyTargets(t *testing.T) {
	// Should not panic or error on empty targets
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	outputOrRun(false, nil)

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom() error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty targets, got %q", buf.String())
	}
}

func TestGetChangedFiles_FilesFrom(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "files.txt")
	if err := os.WriteFile(path, []byte("a.go\nb.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := cliConfig{filesFrom: path}
	got, err := getChangedFiles(cfg, false)
	if err != nil {
		t.Fatalf("getChangedFiles() error: %v", err)
	}
	want := []string{"a.go", "b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getChangedFiles() = %v, want %v", got, want)
	}
}

func TestGetChangedFiles_FilesFromMissing(t *testing.T) {
	cfg := cliConfig{filesFrom: "/nonexistent/file.txt"}
	_, err := getChangedFiles(cfg, false)
	if err == nil {
		t.Fatal("expected error for missing files-from file")
	}
}

func TestCollectAllTests_ErrorPropagation(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir)

	// Create a querier that will fail (default is fail-on-error)
	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "query", "kind('.*_test rule', //pkg/foo:*)").
		WillFail("query error", 1).
		Build()
	q := query.NewBazelQuerierWithExecutor(mockExec)

	_, err := collectAllTests([]string{"//pkg/foo"}, q, c, "", false)
	if err == nil {
		t.Fatal("expected error from collectAllTests when query fails")
	}
}

func TestGetPackageTests_QueryError(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir)

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "query", "kind('.*_test rule', //pkg/foo:*)").
		WillFail("query error", 1).
		Build()
	q := query.NewBazelQuerierWithExecutor(mockExec)

	_, err := getPackageTests("//pkg/foo", q, c, "", false)
	if err == nil {
		t.Fatal("expected error from getPackageTests when query fails")
	}
}
