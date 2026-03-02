package query

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindBazelPackage(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test directory structure
	if err := os.MkdirAll(filepath.Join(tmpDir, "src", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "src", "lib", "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Create(filepath.Join(tmpDir, "BUILD.bazel")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Create(filepath.Join(tmpDir, "src", "BUILD")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Create(filepath.Join(tmpDir, "src", "lib", "BUILD.bazel")); err != nil {
		t.Fatal(err)
	}
	// These files don't need error handling for test purposes
	_, _ = os.Create(filepath.Join(tmpDir, "src", "lib", "file.go"))
	_, _ = os.Create(filepath.Join(tmpDir, "src", "lib", "subdir", "file.go"))
	_, _ = os.Create(filepath.Join(tmpDir, "src", "no_build", "file.go"))

	tests := []struct {
		name      string
		file      string
		wantPkg   string
		wantFound bool
	}{
		{
			name:      "file in root",
			file:      "main.go",
			wantPkg:   "//",
			wantFound: true,
		},
		{
			name:      "file in src",
			file:      "src/main.go",
			wantPkg:   "//src",
			wantFound: true,
		},
		{
			name:      "file in lib",
			file:      "src/lib/file.go",
			wantPkg:   "//src/lib",
			wantFound: true,
		},
		{
			name:      "file in subdir without BUILD",
			file:      "src/lib/subdir/file.go",
			wantPkg:   "//src/lib",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, found := FindBazelPackage(tmpDir, tt.file)
			if found != tt.wantFound {
				t.Errorf("FindBazelPackage(%q, %q) found = %v, want %v", tmpDir, tt.file, found, tt.wantFound)
			}
			if found && pkg != tt.wantPkg {
				t.Errorf("FindBazelPackage(%q, %q) = %q, want %q", tmpDir, tt.file, pkg, tt.wantPkg)
			}
		})
	}
}
