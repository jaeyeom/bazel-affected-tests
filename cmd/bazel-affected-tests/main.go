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

	if countSourceFlags(cfg) > 1 {
		fmt.Fprintln(os.Stderr, "Error: --staged, --head, --base, and --files-from are mutually exclusive")
		os.Exit(1)
	}

	piped := isPipe()
	if countSourceFlags(cfg) > 0 && piped {
		fmt.Fprintln(os.Stderr, "Warning: stdin is a pipe but an explicit flag is set; ignoring pipe input")
	}

	changedFiles, err := getChangedFiles(cfg, piped)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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

	querier := newQuerier(repoCfg)
	allTests := collectAllTests(packages, querier, c, cacheKey, cfg.noCache)

	var configTargets []string
	if repoCfg != nil {
		allTests = repoCfg.FilterExcluded(allTests)
		configTargets = repoCfg.MatchTargets(changedFiles)
		slog.Debug("Config targets matched", "count", len(configTargets))
	}

	// Merge and deduplicate
	allTargets := mergeTargets(allTests, configTargets)
	outputOrRun(cfg.run, allTargets)
}

// outputOrRun either prints the targets to stdout or runs bazel test with them.
func outputOrRun(run bool, targets []string) {
	if !run {
		for _, target := range targets {
			fmt.Println(target)
		}
		return
	}

	if len(targets) == 0 {
		return
	}

	exitCode, err := runBazelTest(executor.NewBasicExecutor(), targets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running bazel test: %v\n", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

type cliConfig struct {
	debug      bool
	cacheDir   string
	clearCache bool
	noCache    bool
	filesFrom  string
	staged     bool
	head       bool
	base       string
	run        bool
}

func parseFlags() cliConfig {
	var cfg cliConfig
	flag.BoolVar(&cfg.debug, "debug", false, "Enable debug output")
	flag.StringVar(&cfg.cacheDir, "cache-dir", "", "Cache directory (default: $HOME/.cache/bazel-affected-tests)")
	flag.BoolVar(&cfg.clearCache, "clear-cache", false, "Clear the cache and exit")
	flag.BoolVar(&cfg.noCache, "no-cache", false, "Disable caching")
	flag.StringVar(&cfg.filesFrom, "files-from", "", "Read changed file list from a file (use - for stdin)")
	flag.BoolVar(&cfg.staged, "staged", false, "Use staged files only (git diff --cached)")
	flag.BoolVar(&cfg.head, "head", false, "Use staged + unstaged files (git diff HEAD)")
	flag.StringVar(&cfg.base, "base", "", "Use all changes vs a ref (git diff <ref>)")
	flag.BoolVar(&cfg.run, "run", false, "Run bazel test with affected targets instead of printing them")
	flag.Parse()

	// Set debug from environment if not set via flag
	if !cfg.debug && os.Getenv("DEBUG") != "" {
		cfg.debug = true
	}

	return cfg
}

// countSourceFlags returns how many file-source flags were explicitly set.
func countSourceFlags(cfg cliConfig) int {
	n := 0
	if cfg.staged {
		n++
	}
	if cfg.head {
		n++
	}
	if cfg.base != "" {
		n++
	}
	if cfg.filesFrom != "" {
		n++
	}
	return n
}

// isPipe reports whether stdin is connected to a pipe (not a terminal).
func isPipe() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) == 0
}

func newQuerier(repoCfg *config.Config) *query.BazelQuerier {
	q := query.NewBazelQuerier()
	if repoCfg != nil {
		q.SetEnableSubpackageQuery(repoCfg.SubpackageQueryEnabled())
	}
	return q
}

func handleCacheClear(c *cache.Cache) error {
	if err := c.Clear(); err != nil {
		return fmt.Errorf("clearing cache: %w", err)
	}
	slog.Debug("Cache cleared successfully")
	return nil
}

func getChangedFiles(cfg cliConfig, piped bool) ([]string, error) {
	ctx := context.Background()
	exec := executor.NewBasicExecutor()

	switch {
	case cfg.filesFrom != "":
		files, err := readFilesFrom(cfg.filesFrom)
		if err != nil {
			return nil, fmt.Errorf("reading files from %q: %w", cfg.filesFrom, err)
		}
		slog.Debug("Read files from input", "source", cfg.filesFrom, "count", len(files))
		return files, nil
	case cfg.staged:
		files, err := git.GetStagedFiles(ctx, exec)
		if err != nil {
			return nil, fmt.Errorf("getting staged files: %w", err)
		}
		slog.Debug("Staged files found", "count", len(files))
		return files, nil
	case cfg.head:
		files, err := git.GetHeadFiles(ctx, exec)
		if err != nil {
			return nil, fmt.Errorf("getting HEAD diff files: %w", err)
		}
		slog.Debug("HEAD diff files found", "count", len(files))
		return files, nil
	case cfg.base != "":
		files, err := git.GetDiffFiles(ctx, exec, cfg.base)
		if err != nil {
			return nil, fmt.Errorf("getting diff files vs %q: %w", cfg.base, err)
		}
		slog.Debug("Base diff files found", "base", cfg.base, "count", len(files))
		return files, nil
	default:
		// Auto mode: pipe → staged → HEAD
		if piped {
			files, err := readFilesFrom("-")
			if err != nil {
				return nil, fmt.Errorf("reading from stdin pipe: %w", err)
			}
			slog.Debug("Read files from pipe", "count", len(files))
			return files, nil
		}
		files, err := git.GetStagedFiles(ctx, exec)
		if err != nil {
			return nil, fmt.Errorf("getting staged files: %w", err)
		}
		if len(files) > 0 {
			slog.Debug("Auto: using staged files", "count", len(files))
			return files, nil
		}
		files, err = git.GetHeadFiles(ctx, exec)
		if err != nil {
			return nil, fmt.Errorf("getting HEAD diff files: %w", err)
		}
		slog.Debug("Auto: using HEAD diff files", "count", len(files))
		return files, nil
	}
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

// mergeTargets deduplicates and sorts the given test and config targets.
func mergeTargets(tests []string, configTargets []string) []string {
	allTargets := make(map[string]bool)
	for _, test := range tests {
		allTargets[test] = true
	}
	for _, target := range configTargets {
		allTargets[target] = true
	}

	result := make([]string, 0, len(allTargets))
	for target := range allTargets {
		result = append(result, target)
	}
	sort.Strings(result)
	return result
}

// runBazelTest executes bazel test with the given targets and returns the exit code.
func runBazelTest(exec executor.Executor, targets []string) (int, error) {
	ctx := context.Background()

	args := append([]string{"test"}, targets...)

	result, err := exec.Execute(ctx, executor.ToolConfig{
		Command:        "bazel",
		Args:           args,
		CommandBuilder: &executor.ShellCommandBuilder{},
	})
	if err != nil {
		return 1, fmt.Errorf("executing bazel test: %w", err)
	}

	if result.Output != "" {
		fmt.Print(result.Output)
	}
	if result.Stderr != "" {
		fmt.Fprint(os.Stderr, result.Stderr)
	}

	return result.ExitCode, nil
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
