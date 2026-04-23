package query

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindBazelPackage(t *testing.T) {
	tmpDir := t.TempDir()

	// Directory structure:
	//   BUILD.bazel                        (root package //)
	//   src/BUILD                          (//src)
	//   src/lib/BUILD.bazel                (//src/lib)
	//   src/lib/subdir/file.go             (1 hop up to //src/lib)
	//   src/no_build/file.go               (1 hop up to //src)
	//   src/no_build/deep/file.go          (2 hops up to //src)
	//   main.go                            (file in //)
	for _, dir := range []string{
		filepath.Join("src", "lib", "subdir"),
		filepath.Join("src", "no_build", "deep"),
	} {
		if err := os.MkdirAll(filepath.Join(tmpDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, buildFile := range []string{
		"BUILD.bazel",
		filepath.Join("src", "BUILD"),
		filepath.Join("src", "lib", "BUILD.bazel"),
	} {
		if _, err := os.Create(filepath.Join(tmpDir, buildFile)); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{
		filepath.Join("src", "lib", "file.go"),
		filepath.Join("src", "lib", "subdir", "file.go"),
		filepath.Join("src", "no_build", "file.go"),
		filepath.Join("src", "no_build", "deep", "file.go"),
	} {
		if _, err := os.Create(filepath.Join(tmpDir, f)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name      string
		file      string
		maxDepth  int
		wantPkg   string
		wantFound bool
	}{
		{
			name:      "file in root with depth 0",
			file:      "main.go",
			maxDepth:  0,
			wantPkg:   "//",
			wantFound: true,
		},
		{
			name:      "file in src with depth 0",
			file:      "src/main.go",
			maxDepth:  0,
			wantPkg:   "//src",
			wantFound: true,
		},
		{
			name:      "file in lib with depth 0",
			file:      "src/lib/file.go",
			maxDepth:  0,
			wantPkg:   "//src/lib",
			wantFound: true,
		},
		{
			name:      "subdir without BUILD resolves within depth 1",
			file:      "src/lib/subdir/file.go",
			maxDepth:  1,
			wantPkg:   "//src/lib",
			wantFound: true,
		},
		{
			name:      "subdir without BUILD rejected at depth 0",
			file:      "src/lib/subdir/file.go",
			maxDepth:  0,
			wantFound: false,
		},
		{
			name:      "no_build resolves to //src within depth 1",
			file:      "src/no_build/file.go",
			maxDepth:  1,
			wantPkg:   "//src",
			wantFound: true,
		},
		{
			name:      "deep no_build rejected at depth 1",
			file:      "src/no_build/deep/file.go",
			maxDepth:  1,
			wantFound: false,
		},
		{
			name:      "deep no_build resolves at depth 2",
			file:      "src/no_build/deep/file.go",
			maxDepth:  2,
			wantPkg:   "//src",
			wantFound: true,
		},
		{
			name:      "unlimited depth walks to root",
			file:      "src/no_build/deep/file.go",
			maxDepth:  UnlimitedParentDepth,
			wantPkg:   "//src",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, found := FindBazelPackage(tmpDir, tt.file, tt.maxDepth)
			if found != tt.wantFound {
				t.Errorf("FindBazelPackage(%q, %q, %d) found = %v, want %v", tmpDir, tt.file, tt.maxDepth, found, tt.wantFound)
			}
			if found && pkg != tt.wantPkg {
				t.Errorf("FindBazelPackage(%q, %q, %d) = %q, want %q", tmpDir, tt.file, tt.maxDepth, pkg, tt.wantPkg)
			}
		})
	}
}
