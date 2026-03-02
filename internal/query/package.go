package query

import (
	"os"
	"path/filepath"
	"strings"
)

// FindBazelPackage finds the nearest Bazel package for a given file.
// A Bazel package is a directory containing a BUILD or BUILD.bazel file.
// filePath is a relative path (e.g., "src/lib/file.go") and repoRoot is the
// absolute path to the repository root.
func FindBazelPackage(repoRoot, filePath string) (string, bool) {
	dir := filepath.Dir(filePath)

	for dir != "." && dir != "/" && dir != "" {
		if hasBuildFile(filepath.Join(repoRoot, dir)) {
			return "//" + strings.ReplaceAll(dir, string(filepath.Separator), "/"), true
		}
		dir = filepath.Dir(dir)
	}

	if hasBuildFile(repoRoot) {
		return "//", true
	}

	return "", false
}

func hasBuildFile(dir string) bool {
	_, err1 := os.Stat(filepath.Join(dir, "BUILD"))
	_, err2 := os.Stat(filepath.Join(dir, "BUILD.bazel"))
	return err1 == nil || err2 == nil
}
