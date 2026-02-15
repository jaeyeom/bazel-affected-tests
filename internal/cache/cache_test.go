package cache

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewCache(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		wantPath string
	}{
		{
			name:     "custom directory",
			dir:      "/tmp/custom-cache",
			wantPath: "/tmp/custom-cache",
		},
		{
			name:     "empty directory uses default",
			dir:      "",
			wantPath: filepath.Join(os.Getenv("HOME"), ".cache", "bazel-affected-tests"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCache(tt.dir, false)
			if c.dir != tt.wantPath {
				t.Errorf("NewCache() dir = %v, want %v", c.dir, tt.wantPath)
			}
		})
	}
}

func TestCache_GetCacheKey(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "cache-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Save current dir and change to temp dir
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(origDir); err != nil {
			t.Fatal(err)
		}
	}()

	// Test with no BUILD files
	c := NewCache("", false)
	key1, err := c.GetCacheKey()
	if err != nil {
		t.Fatalf("GetCacheKey() error = %v", err)
	}
	if key1 == "" {
		t.Error("GetCacheKey() returned empty key")
	}

	// Create a BUILD file
	if err := os.WriteFile("BUILD", []byte("# test build file"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Key should change after adding BUILD file
	key2, err := c.GetCacheKey()
	if err != nil {
		t.Fatalf("GetCacheKey() error = %v", err)
	}
	if key1 == key2 {
		t.Error("GetCacheKey() returned same key after adding BUILD file")
	}

	// Modify the BUILD file
	if err := os.WriteFile("BUILD", []byte("# modified build file"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Key should change after modifying BUILD file
	key3, err := c.GetCacheKey()
	if err != nil {
		t.Fatalf("GetCacheKey() error = %v", err)
	}
	if key2 == key3 {
		t.Error("GetCacheKey() returned same key after modifying BUILD file")
	}

	// Create nested BUILD files
	if err := os.MkdirAll("src/lib", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/BUILD.bazel", []byte("# src build"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Key should change after adding more BUILD files
	key4, err := c.GetCacheKey()
	if err != nil {
		t.Fatalf("GetCacheKey() error = %v", err)
	}
	if key3 == key4 {
		t.Error("GetCacheKey() returned same key after adding more BUILD files")
	}

	// Create a .bzl file
	if err := os.WriteFile("defs.bzl", []byte("# macro definitions"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Key should change after adding .bzl file
	key5, err := c.GetCacheKey()
	if err != nil {
		t.Fatalf("GetCacheKey() error = %v", err)
	}
	if key4 == key5 {
		t.Error("GetCacheKey() returned same key after adding .bzl file")
	}

	// Modify the .bzl file
	if err := os.WriteFile("defs.bzl", []byte("# modified macros"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Key should change after modifying .bzl file
	key6, err := c.GetCacheKey()
	if err != nil {
		t.Fatalf("GetCacheKey() error = %v", err)
	}
	if key5 == key6 {
		t.Error("GetCacheKey() returned same key after modifying .bzl file")
	}

	// Create WORKSPACE file (should NOT affect cache key)
	if err := os.WriteFile("WORKSPACE", []byte("# workspace"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Key should NOT change after adding WORKSPACE file
	key7, err := c.GetCacheKey()
	if err != nil {
		t.Fatalf("GetCacheKey() error = %v", err)
	}
	if key6 != key7 {
		t.Error("GetCacheKey() should return same key after adding WORKSPACE file")
	}

	// Create MODULE.bazel file (should NOT affect cache key)
	if err := os.WriteFile("MODULE.bazel", []byte("# module"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Key should NOT change after adding MODULE file
	key8, err := c.GetCacheKey()
	if err != nil {
		t.Fatalf("GetCacheKey() error = %v", err)
	}
	if key7 != key8 {
		t.Error("GetCacheKey() should return same key after adding MODULE.bazel file")
	}

	// The following subtests continue with the cumulative file state above.
	// They share the CWD (tmpDir) and cache instance, so they run sequentially
	// via t.Run (no t.Parallel) to avoid races.

	t.Run("WORKSPACE.bazel does not affect key", func(t *testing.T) {
		if err := os.WriteFile("WORKSPACE.bazel", []byte("# workspace bazel"), 0o600); err != nil {
			t.Fatal(err)
		}
		key9, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if key8 != key9 {
			t.Error("GetCacheKey() should return same key after adding WORKSPACE.bazel file")
		}
	})

	t.Run("MODULE plain does not affect key", func(t *testing.T) {
		if err := os.WriteFile("MODULE", []byte("# module plain"), 0o600); err != nil {
			t.Fatal(err)
		}
		key, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if key8 != key {
			t.Error("GetCacheKey() should return same key after adding MODULE file")
		}
	})

	t.Run("unrelated files do not affect key", func(t *testing.T) {
		if err := os.WriteFile("README.md", []byte("notes"), 0o600); err != nil {
			t.Fatal(err)
		}
		key, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if key8 != key {
			t.Error("GetCacheKey() should return same key after adding unrelated file")
		}
	})

	t.Run("rename BUILD to BUILD.bazel changes key", func(t *testing.T) {
		if err := os.MkdirAll("rename_case", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("rename_case/BUILD", []byte("# same content"), 0o600); err != nil {
			t.Fatal(err)
		}
		before, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if err := os.Rename("rename_case/BUILD", "rename_case/BUILD.bazel"); err != nil {
			t.Fatal(err)
		}
		after, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if before == after {
			t.Error("GetCacheKey() should change after renaming BUILD to BUILD.bazel")
		}
	})

	t.Run("unstaged BUILD edit changes key", func(t *testing.T) {
		before, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if err := os.WriteFile("src/BUILD.bazel", []byte("# src build changed unstaged"), 0o600); err != nil {
			t.Fatal(err)
		}
		after, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if before == after {
			t.Error("GetCacheKey() should change after unstaged BUILD edit")
		}
	})

	t.Run("nested .bzl changes affect key", func(t *testing.T) {
		if err := os.WriteFile("src/lib/rules.bzl", []byte("# nested bzl"), 0o600); err != nil {
			t.Fatal(err)
		}
		key1, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if err := os.WriteFile("src/lib/rules.bzl", []byte("# nested bzl modified"), 0o600); err != nil {
			t.Fatal(err)
		}
		key2, err := c.GetCacheKey()
		if err != nil {
			t.Fatalf("GetCacheKey() error = %v", err)
		}
		if key1 == key2 {
			t.Error("GetCacheKey() should change after modifying nested .bzl file")
		}
	})
}

func TestCache_SetAndGet(t *testing.T) {
	// Create a temporary cache directory
	tmpDir, err := os.MkdirTemp("", "cache-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	c := NewCache(tmpDir, false)
	cacheKey := "test-cache-key"

	tests := []struct {
		name  string
		pkg   string
		tests []string
	}{
		{
			name:  "root package",
			pkg:   "//",
			tests: []string{"//test:test1", "//test:test2"},
		},
		{
			name:  "nested package",
			pkg:   "//src/lib",
			tests: []string{"//src/lib:test1", "//other:test2"},
		},
		{
			name:  "empty tests",
			pkg:   "//empty",
			tests: []string{},
		},
		{
			name:  "package with colon",
			pkg:   "//pkg:target",
			tests: []string{"//pkg:test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set cache
			if err := c.Set(cacheKey, tt.pkg, tt.tests); err != nil {
				t.Fatalf("Set() error = %v", err)
			}

			// Get cache
			got, found := c.Get(cacheKey, tt.pkg)
			if !found {
				t.Fatal("Get() returned not found")
			}
			if !reflect.DeepEqual(got, tt.tests) {
				t.Errorf("Get() = %v, want %v", got, tt.tests)
			}

			// Try with different cache key - should not be found
			_, found = c.Get("different-key", tt.pkg)
			if found {
				t.Error("Get() with different key should return not found")
			}
		})
	}
}

func TestCache_Clear(t *testing.T) {
	// Create a temporary cache directory
	tmpDir, err := os.MkdirTemp("", "cache-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	c := NewCache(tmpDir, false)
	cacheKey := "test-cache-key"

	// Set some cache data
	if err := c.Set(cacheKey, "//test", []string{"//test:test1"}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// Verify cache exists
	if _, found := c.Get(cacheKey, "//test"); !found {
		t.Fatal("Cache should exist before clear")
	}

	// Clear cache
	if err := c.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	// Verify cache directory is removed
	if _, err := os.Stat(tmpDir); !errors.Is(err, fs.ErrNotExist) {
		t.Error("Clear() should remove cache directory")
	}

	// Get should return not found after clear
	if _, found := c.Get(cacheKey, "//test"); found {
		t.Error("Get() should return not found after Clear()")
	}
}

func TestCache_getCacheFile(t *testing.T) {
	c := NewCache("/tmp/cache", false)

	tests := []struct {
		name     string
		cacheKey string
		pkg      string
		wantFile string
	}{
		{
			name:     "root package",
			cacheKey: "abc123",
			pkg:      "//",
			wantFile: "/tmp/cache/abc123/root.json",
		},
		{
			name:     "simple package",
			cacheKey: "abc123",
			pkg:      "//src",
			wantFile: "/tmp/cache/abc123/src.json",
		},
		{
			name:     "nested package",
			cacheKey: "abc123",
			pkg:      "//src/lib",
			wantFile: "/tmp/cache/abc123/src__lib.json",
		},
		{
			name:     "package with target",
			cacheKey: "abc123",
			pkg:      "//src:target",
			wantFile: "/tmp/cache/abc123/src__target.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.getCacheFile(tt.cacheKey, tt.pkg)
			if got != tt.wantFile {
				t.Errorf("getCacheFile() = %v, want %v", got, tt.wantFile)
			}
		})
	}
}

func TestCache_InvalidJSON(t *testing.T) {
	// Create a temporary cache directory
	tmpDir, err := os.MkdirTemp("", "cache-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	c := NewCache(tmpDir, false)
	cacheKey := "test-cache-key"

	// Create cache directory
	cacheDir := filepath.Join(tmpDir, cacheKey)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write invalid JSON
	cacheFile := filepath.Join(cacheDir, "root.json")
	if err := os.WriteFile(cacheFile, []byte("invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Get should return not found for invalid JSON
	_, found := c.Get(cacheKey, "//")
	if found {
		t.Error("Get() should return not found for invalid JSON")
	}
}

func TestCache_PermissionError(t *testing.T) {
	// Skip on Windows where permission model is different
	if os.Getenv("GOOS") == "windows" {
		t.Skip("Skipping permission test on Windows")
	}

	// Create a temporary cache directory
	tmpDir, err := os.MkdirTemp("", "cache-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	c := NewCache(tmpDir, false)
	cacheKey := "test-cache-key"

	// Create read-only cache directory
	cacheDir := filepath.Join(tmpDir, cacheKey)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cacheDir, 0o555); err != nil {
		t.Fatal(err)
	}

	// Set should fail with permission error
	err = c.Set(cacheKey, "//test", []string{"//test:test1"})
	if err == nil {
		t.Error("Set() should fail with permission error")
	}
}

func TestCache_DebugOutput(t *testing.T) {
	// This test verifies debug output is called but doesn't check the actual output
	// since it goes to stdout. In a real implementation, you might want to capture stdout.

	tmpDir, err := os.MkdirTemp("", "cache-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	c := NewCache(tmpDir, true) // Enable debug
	cacheKey := "test-cache-key"

	// These operations should produce debug output
	_ = c.Set(cacheKey, "//test", []string{"//test:test1"})
	c.Get(cacheKey, "//test")
	c.Get(cacheKey, "//nonexistent")
}
