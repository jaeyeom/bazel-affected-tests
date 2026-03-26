package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"

	"github.com/jaeyeom/bazel-affected-tests/internal/cache"
	"github.com/jaeyeom/bazel-affected-tests/internal/config"
	"github.com/jaeyeom/bazel-affected-tests/internal/git"
	"github.com/jaeyeom/bazel-affected-tests/internal/query"
	executor "github.com/jaeyeom/go-cmdexec"
)

func main() {
	cfg := parseFlags()

	if cfg.debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting working directory: %v\n", err)
		os.Exit(1)
	}

	c := cache.NewCache(cfg.cacheDir)

	if cfg.clearCache {
		if err := handleCacheClear(c); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	var changedFiles []string
	if cfg.filesFrom != "" {
		var err error
		changedFiles, err = readFilesFrom(cfg.filesFrom)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading files from %q: %v\n", cfg.filesFrom, err)
			os.Exit(1)
		}
		slog.Debug("Read files from input", "source", cfg.filesFrom, "count", len(changedFiles))
	} else {
		var err error
		changedFiles, err = getStagedFiles()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting staged files: %v\n", err)
			os.Exit(1)
		}
		slog.Debug("Staged files found", "count", len(changedFiles))
	}

	if len(changedFiles) == 0 {
		os.Exit(0)
	}

	// Load config early so ignore_paths can filter files before package resolution
	repoCfg, err := config.LoadConfig(repoRoot)
	if err != nil {
		slog.Warn("Failed to load config", "error", err)
	}

	if repoCfg != nil {
		changedFiles = repoCfg.FilterIgnoredFiles(changedFiles)
		slog.Debug("Files after ignore_paths filtering", "count", len(changedFiles))
		if len(changedFiles) == 0 {
			os.Exit(0)
		}
	}

	packages := findPackages(repoRoot, changedFiles)
	if len(packages) == 0 {
		slog.Debug("No Bazel packages found for staged files")
		os.Exit(0)
	}

	cacheKey := getCacheKey(c, cfg.noCache, repoRoot)

	querier := query.NewBazelQuerier()
	allTests := collectAllTests(packages, querier, c, cacheKey, cfg.noCache)

	var configTargets []string
	if repoCfg != nil {
		allTests = repoCfg.FilterExcluded(allTests)
		configTargets = repoCfg.MatchTargets(changedFiles)
		slog.Debug("Config targets matched", "count", len(configTargets))
	}

	// Merge and output
	outputResults(allTests, configTargets)
}

type cliConfig struct {
	debug      bool
	cacheDir   string
	clearCache bool
	noCache    bool
	filesFrom  string
}

func parseFlags() cliConfig {
	var cfg cliConfig
	flag.BoolVar(&cfg.debug, "debug", false, "Enable debug output")
	flag.StringVar(&cfg.cacheDir, "cache-dir", "", "Cache directory (default: $HOME/.cache/bazel-affected-tests)")
	flag.BoolVar(&cfg.clearCache, "clear-cache", false, "Clear the cache and exit")
	flag.BoolVar(&cfg.noCache, "no-cache", false, "Disable caching")
	flag.StringVar(&cfg.filesFrom, "files-from", "", "Read changed file list from a file (use - for stdin)")
	flag.Parse()

	// Set debug from environment if not set via flag
	if !cfg.debug && os.Getenv("DEBUG") != "" {
		cfg.debug = true
	}

	return cfg
}

func handleCacheClear(c *cache.Cache) error {
	if err := c.Clear(); err != nil {
		return fmt.Errorf("clearing cache: %w", err)
	}
	slog.Debug("Cache cleared successfully")
	return nil
}

func getStagedFiles() ([]string, error) {
	ctx := context.Background()
	exec := executor.NewBasicExecutor()
	files, err := git.GetStagedFiles(ctx, exec)
	if err != nil {
		return nil, fmt.Errorf("getting staged files: %w", err)
	}
	return files, nil
}

func getCacheKey(c *cache.Cache, noCache bool, repoRoot string) string {
	if noCache {
		return ""
	}

	cacheKey, err := c.GetCacheKey(repoRoot)
	if err != nil {
		slog.Debug("Failed to compute cache key", "error", err)
		return ""
	}

	slog.Debug("Cache key computed", "key", cacheKey)
	return cacheKey
}

func findPackages(repoRoot string, changedFiles []string) []string {
	packageMap := make(map[string]bool)
	for _, file := range changedFiles {
		slog.Debug("Processing file", "file", file)
		if pkg, found := query.FindBazelPackage(repoRoot, file); found {
			slog.Debug("Found package", "package", pkg)
			packageMap[pkg] = true
		} else {
			slog.Debug("No Bazel package found for file", "file", file)
		}
	}

	var packages []string
	for pkg := range packageMap {
		packages = append(packages, pkg)
	}
	return packages
}

func collectAllTests(packages []string, querier *query.BazelQuerier, c *cache.Cache, cacheKey string, noCache bool) []string {
	allTestsMap := make(map[string]bool)

	// Process packages
	for _, pkg := range packages {
		tests := getPackageTests(pkg, querier, c, cacheKey, noCache)
		for _, test := range tests {
			allTestsMap[test] = true
		}
	}

	var allTests []string
	for test := range allTestsMap {
		allTests = append(allTests, test)
	}
	return allTests
}

func getPackageTests(pkg string, querier *query.BazelQuerier, c *cache.Cache, cacheKey string, noCache bool) []string {
	if !noCache && cacheKey != "" {
		if cachedTests, found := c.Get(cacheKey, pkg); found {
			return cachedTests
		}
	}

	tests, err := querier.FindAffectedTests([]string{pkg})
	if err != nil {
		slog.Debug("Error querying tests for package", "package", pkg, "error", err)
		return nil
	}

	// Store in cache
	if !noCache && cacheKey != "" {
		if err := c.Set(cacheKey, pkg, tests); err != nil {
			slog.Debug("Failed to cache results", "package", pkg, "error", err)
		}
	}

	return tests
}

func outputResults(tests []string, configTargets []string) {
	// Merge and deduplicate
	allTargets := make(map[string]bool)
	for _, test := range tests {
		allTargets[test] = true
	}
	for _, target := range configTargets {
		allTargets[target] = true
	}

	// Convert to sorted slice
	result := make([]string, 0, len(allTargets))
	for target := range allTargets {
		result = append(result, target)
	}
	sort.Strings(result)

	for _, target := range result {
		fmt.Println(target)
	}
}

// readFilesFrom reads non-empty lines from the given path. If path is "-",
// it reads from stdin.
func readFilesFrom(path string) ([]string, error) {
	var r *os.File
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("opening file: %w", err)
		}
		defer f.Close()
		r = f
	}

	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading lines: %w", err)
	}
	return lines, nil
}
