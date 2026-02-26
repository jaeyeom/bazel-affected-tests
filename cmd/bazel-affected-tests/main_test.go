package main

import (
	"reflect"
	"slices"
	"testing"

	"github.com/jaeyeom/bazel-affected-tests/internal/cache"
	"github.com/jaeyeom/bazel-affected-tests/internal/query"
	executor "github.com/jaeyeom/go-cmdexec"
)

func sorted(xs []string) []string {
	out := slices.Clone(xs)
	slices.Sort(out)
	return out
}

func TestGetPackageTests_UsesCacheWhenAvailable(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir, false)
	cacheKey := "k1"
	pkg := "//pkg/foo"
	cached := []string{"//pkg/foo:cached_test"}
	if err := c.Set(cacheKey, pkg, cached); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	mockExec := executor.NewMockExecutor()
	q := query.NewBazelQuerierWithExecutor(mockExec, false)

	got := getPackageTests(pkg, q, c, cacheKey, false)
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
	c := cache.NewCache(tmpDir, false)
	cacheKey := "k1"
	pkg := "//pkg/foo"

	// FindAffectedTests internally makes 2 bazel queries per package:
	// 1. kind('.*_test rule', PKG:*)         — same-package tests
	// 2. rdeps(//..., PKG:*) intersect ...   — reverse-dep tests
	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--noblock_for_lock", "kind('.*_test rule', //pkg/foo:*)").
		WillSucceed("//pkg/foo:unit_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--noblock_for_lock", "rdeps(//..., //pkg/foo:*) intersect kind('.*_test rule', //...)").
		WillSucceed("//dep:dep_test", 0).
		Once().
		Build()
	q := query.NewBazelQuerierWithExecutor(mockExec, false)

	got := sorted(getPackageTests(pkg, q, c, cacheKey, false))
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
	c := cache.NewCache(tmpDir, false)
	cacheKey := "k1"
	pkg := "//pkg/foo"
	if err := c.Set(cacheKey, pkg, []string{"//pkg/foo:old_cached"}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--noblock_for_lock", "kind('.*_test rule', //pkg/foo:*)").
		WillSucceed("//pkg/foo:new_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--noblock_for_lock", "rdeps(//..., //pkg/foo:*) intersect kind('.*_test rule', //...)").
		WillSucceed("", 0).
		Once().
		Build()
	q := query.NewBazelQuerierWithExecutor(mockExec, false)

	got := getPackageTests(pkg, q, c, cacheKey, true)
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
	c := cache.NewCache(tmpDir, false)
	pkg := "//pkg/foo"

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--noblock_for_lock", "kind('.*_test rule', //pkg/foo:*)").
		WillSucceed("//pkg/foo:new_test", 0).
		Once().
		Build()
	mockExec.ExpectCommandWithArgs("bazel", "query", "--noblock_for_lock", "rdeps(//..., //pkg/foo:*) intersect kind('.*_test rule', //...)").
		WillSucceed("", 0).
		Once().
		Build()
	q := query.NewBazelQuerierWithExecutor(mockExec, false)

	got := getPackageTests(pkg, q, c, "", false)
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

func TestCollectAllTests_DeduplicatesAcrossPackages(t *testing.T) {
	tmpDir := t.TempDir()
	c := cache.NewCache(tmpDir, false)
	cacheKey := "k1"
	if err := c.Set(cacheKey, "//pkg/foo", []string{"//pkg/foo:t1", "//shared:t"}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if err := c.Set(cacheKey, "//pkg/bar", []string{"//pkg/bar:t2", "//shared:t"}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	mockExec := executor.NewMockExecutor()
	q := query.NewBazelQuerierWithExecutor(mockExec, false)

	got := sorted(collectAllTests([]string{"//pkg/foo", "//pkg/bar"}, q, c, cacheKey, false))
	want := sorted([]string{"//pkg/foo:t1", "//pkg/bar:t2", "//shared:t"})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collectAllTests() = %v, want %v", got, want)
	}
}
