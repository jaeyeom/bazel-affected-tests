package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/jaeyeom/bazel-affected-tests/internal/audit"
	"github.com/jaeyeom/bazel-affected-tests/internal/cache"
	"github.com/jaeyeom/bazel-affected-tests/internal/git"
	"github.com/jaeyeom/bazel-affected-tests/internal/query"
	executor "github.com/jaeyeom/go-cmdexec"
)

const (
	auditFormatText = "text"
	auditFormatJSON = "json"
)

type auditConfig struct {
	debug    bool
	cacheDir string
	noCache  bool
	timing   bool
	format   string
	patterns []string
}

func parseAuditFlags(args []string) (auditConfig, error) {
	fs := flag.NewFlagSet("audit-packages", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var cfg auditConfig
	fs.BoolVar(&cfg.debug, "debug", false, "Enable debug output")
	fs.StringVar(&cfg.cacheDir, "cache-dir", "", "Cache directory (default: $HOME/.cache/bazel-affected-tests)")
	fs.BoolVar(&cfg.noCache, "no-cache", false, "Disable caching")
	fs.BoolVar(&cfg.timing, "timing", false, "Print per-stage wall-clock durations to stderr")
	fs.StringVar(&cfg.format, "format", auditFormatText, "Output format: text or json")
	if err := fs.Parse(args); err != nil {
		return cfg, fmt.Errorf("parsing audit flags: %w", err)
	}
	cfg.patterns = fs.Args()
	if len(cfg.patterns) == 0 {
		cfg.patterns = []string{"//..."}
	}
	if cfg.format != auditFormatText && cfg.format != auditFormatJSON {
		return cfg, fmt.Errorf("--format must be %q or %q, got %q", auditFormatText, auditFormatJSON, cfg.format)
	}
	return cfg, nil
}

func runAuditPackages(args []string) int {
	cfg, err := parseAuditFlags(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	if cfg.debug {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	timer := newStageTimer(cfg.timing)
	audits, err := executeAudit(cfg, timer)
	timer.report(os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if cfg.format == auditFormatJSON {
		if err := writeAuditJSON(os.Stdout, audits); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
	} else {
		writeAuditText(os.Stdout, audits)
	}
	return 0
}

func executeAudit(cfg auditConfig, timer *stageTimer) ([]*audit.PackageAudit, error) {
	stop := timer.stage("repo-root")
	repoRoot, err := git.RepoRoot(context.Background(), executor.NewBasicExecutor())
	stop()
	if err != nil {
		return nil, fmt.Errorf("not a git repository (or any parent): %w", err)
	}

	c := cache.NewCache(cfg.cacheDir)
	cacheKey := ""
	if !cfg.noCache {
		stop = timer.stage("cache-key")
		if k, err := c.GetCacheKey(repoRoot); err != nil {
			slog.Debug("audit: failed to compute cache key", "error", err)
		} else {
			cacheKey = k
		}
		stop()
	}

	inner := query.NewBazelQuerier()
	var auditQ audit.Querier = inner
	if cacheKey != "" {
		auditQ = newCachingQuerier(inner, c, cacheKey)
	}

	stop = timer.stage("discover-packages")
	packages, err := discoverPackages(inner, cfg.patterns)
	stop()
	if err != nil {
		return nil, err
	}
	slog.Debug("audit: packages discovered", "count", len(packages))
	if len(packages) == 0 {
		return nil, nil
	}

	stop = timer.stage("audit")
	audits, err := audit.NewAuditor(auditQ).AuditPackages(packages)
	stop()
	if err != nil {
		return nil, fmt.Errorf("auditing packages: %w", err)
	}
	return audits, nil
}

func discoverPackages(q *query.BazelQuerier, patterns []string) ([]string, error) {
	seen := make(map[string]bool)
	var packages []string
	for _, p := range patterns {
		pkgs, err := q.QueryPackages(p)
		if err != nil {
			return nil, fmt.Errorf("discovering packages for %s: %w", p, err)
		}
		for _, pkg := range pkgs {
			if seen[pkg] {
				continue
			}
			seen[pkg] = true
			packages = append(packages, pkg)
		}
	}
	sort.Strings(packages)
	return packages, nil
}

// cachingQuerier wraps an audit.Querier with disk-backed caching for
// QueryDeps results. Results are keyed by the workspace BUILD/.bzl hash, so
// closures stay valid until any rule definition changes.
//
// QueryRules is passed through; rule metadata is cheap to re-parse from a
// single bazel query and the structured form would complicate JSON storage.
type cachingQuerier struct {
	inner    audit.Querier
	cache    *cache.Cache
	cacheKey string
}

func newCachingQuerier(inner audit.Querier, c *cache.Cache, key string) *cachingQuerier {
	return &cachingQuerier{inner: inner, cache: c, cacheKey: key}
}

func (c *cachingQuerier) QueryRules(pattern string) ([]query.Rule, error) {
	rules, err := c.inner.QueryRules(pattern)
	if err != nil {
		return nil, fmt.Errorf("querying rules: %w", err)
	}
	return rules, nil
}

func (c *cachingQuerier) QueryDeps(targets []string) ([]string, error) {
	storeKey := depsCacheKey(targets)
	if cached, ok := c.cache.Get(c.cacheKey, storeKey); ok {
		return cached, nil
	}
	deps, err := c.inner.QueryDeps(targets)
	if err != nil {
		return nil, fmt.Errorf("querying deps: %w", err)
	}
	if setErr := c.cache.Set(c.cacheKey, storeKey, deps); setErr != nil {
		slog.Debug("audit cache set failed", "key", storeKey, "error", setErr)
	}
	return deps, nil
}

// depsCacheKey hashes the target list (order-independent) so the cache file
// name is filesystem-safe and short. Including the count plus a stable hash
// prefix keeps collisions astronomically unlikely.
func depsCacheKey(targets []string) string {
	sorted := append([]string(nil), targets...)
	sort.Strings(sorted)
	h := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return fmt.Sprintf("audit-deps-%x", h[:8])
}
