package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

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

	timer := newStageTimer(cfg.timing)
	targets, err := resolveTargets(cfg, c, timer)
	timer.report(os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	outputOrRun(cfg.run, targets)
}

// resolveTargets detects changed files, finds affected Bazel packages, queries
// for affected test targets, and applies config-based filtering and additions.
func resolveTargets(cfg cliConfig, c *cache.Cache, timer *stageTimer) ([]string, error) {
	stop := timer.stage("repo-root")
	repoRoot, err := git.RepoRoot(context.Background(), executor.NewBasicExecutor())
	stop()
	if err != nil {
		return nil, fmt.Errorf("not a git repository (or any parent): %w", err)
	}

	piped := isPipe()
	if countSourceFlags(cfg) > 0 && piped {
		fmt.Fprintln(os.Stderr, "Warning: stdin is a pipe but an explicit flag is set; ignoring pipe input")
	}

	stop = timer.stage("changed-files")
	changedFiles, err := getChangedFiles(cfg, piped)
	stop()
	if err != nil {
		return nil, err
	}

	if len(changedFiles) == 0 {
		return nil, nil
	}

	// Load config early so ignore_paths can filter files before package resolution
	stop = timer.stage("load-config")
	repoCfg, err := config.LoadConfig(repoRoot)
	stop()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if repoCfg != nil {
		changedFiles = repoCfg.FilterIgnoredFiles(changedFiles)
		slog.Debug("Files after ignore_paths filtering", "count", len(changedFiles))
		if len(changedFiles) == 0 {
			return nil, nil
		}
	}

	maxDepth := resolveMaxParentDepth(cfg, repoCfg)
	strict := resolveStrict(cfg, repoCfg)

	changedFiles, err = rejectAbsolutePaths(changedFiles, strict)
	if err != nil {
		return nil, err
	}
	if len(changedFiles) == 0 {
		return nil, nil
	}

	stop = timer.stage("find-packages")
	packages, unmapped := findPackages(repoRoot, changedFiles, maxDepth)
	stop()
	slog.Debug("Bazel packages found", "count", len(packages))
	if len(unmapped) > 0 {
		if strict {
			return nil, fmt.Errorf("files not mapped to any Bazel package within max-parent-depth=%d: %v", maxDepth, unmapped)
		}
		slog.Warn("files not mapped to any Bazel package within max-parent-depth",
			"max_parent_depth", maxDepth, "files", unmapped)
	}

	allTests, err := queryTestsForPackages(cfg, repoCfg, c, repoRoot, packages, timer)
	if err != nil {
		return nil, err
	}

	var configTargets []string
	if repoCfg != nil {
		allTests = repoCfg.FilterExcluded(allTests)
		configTargets = repoCfg.MatchTargets(changedFiles)
		slog.Debug("Config targets matched", "count", len(configTargets))
	}

	return mergeTargets(allTests, configTargets), nil
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

// maxParentDepthUnset is a sentinel value used to detect whether the user
// passed --max-parent-depth on the command line. It is distinct from any
// meaningful value (including -1 for unlimited).
const maxParentDepthUnset = -2

type cliConfig struct {
	debug          bool
	cacheDir       string
	clearCache     bool
	noCache        bool
	filesFrom      string
	staged         bool
	head           bool
	base           string
	run            bool
	bestEffort     bool
	maxParentDepth int
	strict         bool
	strictSet      bool
	timing         bool
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
	flag.BoolVar(&cfg.bestEffort, "best-effort", false, "Log warnings instead of failing on Bazel query errors")
	flag.IntVar(&cfg.maxParentDepth, "max-parent-depth", maxParentDepthUnset,
		"Max parent directories to walk looking for a BUILD file (default 1; -1 for unlimited)")
	flag.BoolVar(&cfg.strict, "strict", false,
		"Fail if any changed file does not map to a Bazel package within max-parent-depth")
	flag.BoolVar(&cfg.timing, "timing", false, "Print per-stage wall-clock durations to stderr")
	flag.BoolVar(&cfg.timing, "profile", false, "Alias for --timing")
	flag.Parse()

	// Record whether --strict was explicitly set so config can override only when it wasn't.
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "strict" {
			cfg.strictSet = true
		}
	})

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

// resolveMaxParentDepth returns the effective max-parent-depth, honoring
// precedence CLI flag > config > DefaultMaxParentDepth.
func resolveMaxParentDepth(cfg cliConfig, repoCfg *config.Config) int {
	if cfg.maxParentDepth != maxParentDepthUnset {
		return cfg.maxParentDepth
	}
	return repoCfg.ResolvedMaxParentDepth(config.DefaultMaxParentDepth)
}

// resolveStrict returns the effective strict value, honoring precedence
// CLI flag > config > false.
func resolveStrict(cfg cliConfig, repoCfg *config.Config) bool {
	if cfg.strictSet {
		return cfg.strict
	}
	if repoCfg != nil {
		return repoCfg.Strict
	}
	return false
}

// partitionAbsolutePaths splits files into those that look like absolute
// paths (leading "/") and the rest. Absolute paths are never legitimate
// inputs because changed-file lists are always repo-relative; treating
// them as relative would silently misroute lookups (e.g., a CODEOWNERS
// pattern like "/bin/tests/**" would map to the repo's bin/tests package).
func partitionAbsolutePaths(files []string) (absolute, relative []string) {
	for _, f := range files {
		if strings.HasPrefix(f, "/") {
			absolute = append(absolute, f)
		} else {
			relative = append(relative, f)
		}
	}
	return absolute, relative
}

// queryTestsForPackages computes the cache key and resolves affected tests
// for the given packages. Returns nil tests when packages is empty.
func queryTestsForPackages(cfg cliConfig, repoCfg *config.Config, c *cache.Cache, repoRoot string, packages []string, timer *stageTimer) ([]string, error) {
	if len(packages) == 0 {
		return nil, nil
	}

	stop := timer.stage("cache-key")
	cacheKey := getCacheKey(c, cfg.noCache, repoRoot)
	stop()

	querier := newQuerier(repoCfg)
	if cfg.bestEffort {
		querier.SetFailOnError(false)
	}
	stop = timer.stage("bazel-query")
	tests, err := collectAllTests(packages, querier, c, cacheKey, cfg.noCache)
	stop()
	return tests, err
}

// rejectAbsolutePaths drops absolute paths from files. In strict mode any
// absolute path is a fatal error; otherwise it logs a warning and continues
// with the remaining repo-relative paths.
func rejectAbsolutePaths(files []string, strict bool) ([]string, error) {
	absolute, relative := partitionAbsolutePaths(files)
	if len(absolute) == 0 {
		return files, nil
	}
	if strict {
		return nil, fmt.Errorf("file paths must be repo-relative; got absolute paths: %v", absolute)
	}
	slog.Warn("ignoring absolute file paths (must be repo-relative)", "files", absolute)
	return relative, nil
}

// findPackages resolves each changed file to its Bazel package, capped at
// maxDepth parent hops. It returns the deduplicated list of packages found
// and the files that did not resolve within the cap.
func findPackages(repoRoot string, changedFiles []string, maxDepth int) (packages, unmapped []string) {
	packageMap := make(map[string]bool)
	for _, file := range changedFiles {
		slog.Debug("Processing file", "file", file)
		if pkg, found := query.FindBazelPackage(repoRoot, file, maxDepth); found {
			slog.Debug("Found package", "package", pkg)
			packageMap[pkg] = true
		} else {
			slog.Debug("No Bazel package found for file", "file", file)
			unmapped = append(unmapped, file)
		}
	}

	for pkg := range packageMap {
		packages = append(packages, pkg)
	}
	return packages, unmapped
}

func collectAllTests(packages []string, querier *query.BazelQuerier, c *cache.Cache, cacheKey string, noCache bool) ([]string, error) {
	allTestsMap := make(map[string]bool)

	// Process packages
	for _, pkg := range packages {
		tests, err := getPackageTests(pkg, querier, c, cacheKey, noCache)
		if err != nil {
			return nil, err
		}
		for _, test := range tests {
			allTestsMap[test] = true
		}
	}

	var allTests []string
	for test := range allTestsMap {
		allTests = append(allTests, test)
	}
	return allTests, nil
}

func getPackageTests(pkg string, querier *query.BazelQuerier, c *cache.Cache, cacheKey string, noCache bool) ([]string, error) {
	if !noCache && cacheKey != "" {
		if cachedTests, found := c.Get(cacheKey, pkg); found {
			return cachedTests, nil
		}
	}

	tests, err := querier.FindAffectedTests([]string{pkg})
	if err != nil {
		return nil, fmt.Errorf("querying tests for package %s: %w", pkg, err)
	}

	// Store in cache
	if !noCache && cacheKey != "" {
		if err := c.Set(cacheKey, pkg, tests); err != nil {
			slog.Debug("Failed to cache results", "package", pkg, "error", err)
		}
	}

	return tests, nil
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
