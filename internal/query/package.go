package query

import (
	"os"
	"path/filepath"
	"strings"
)

// UnlimitedParentDepth disables the max-parent-depth cap in FindBazelPackage.
const UnlimitedParentDepth = -1

// FindBazelPackage finds the nearest Bazel package for a given file.
// A Bazel package is a directory containing a BUILD or BUILD.bazel file.
// filePath is a relative path (e.g., "src/lib/file.go") and repoRoot is the
// absolute path to the repository root.
//
// maxDepth caps how many parent directories above the file's own directory
// may be walked looking for a BUILD file. maxDepth=0 means only the file's
// own directory is considered. maxDepth=UnlimitedParentDepth (-1) disables
// the cap and walks all the way to the repo root.
func FindBazelPackage(repoRoot, filePath string, maxDepth int) (string, bool) {
	dir := filepath.Dir(filePath)
	hops := 0

	for dir != "." && dir != "/" && dir != "" {
		if maxDepth != UnlimitedParentDepth && hops > maxDepth {
			return "", false
		}
		if hasBuildFile(filepath.Join(repoRoot, dir)) {
			return "//" + strings.ReplaceAll(dir, string(filepath.Separator), "/"), true
		}
		dir = filepath.Dir(dir)
		hops++
	}

	if maxDepth != UnlimitedParentDepth && hops > maxDepth {
		return "", false
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
