package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/jaeyeom/bazel-affected-tests/internal/audit"
)

// writeAuditText prints the human-readable audit, ranked by severity. Only
// packages with at least one source file appear in the report.
func writeAuditText(w io.Writer, audits []*audit.PackageAudit) {
	ranked := rankAudits(audits)
	if len(ranked) == 0 {
		fmt.Fprintln(w, "Package cohesion audit: no packages with source files found")
		return
	}
	fmt.Fprintln(w, "Package cohesion audit")
	fmt.Fprintln(w)
	for _, a := range ranked {
		writeAuditPackageText(w, a)
	}
}

func writeAuditPackageText(w io.Writer, a *audit.PackageAudit) {
	fmt.Fprintln(w, a.Package)
	fmt.Fprintf(w, "  rules: %d\n", a.RuleCount)
	fmt.Fprintf(w, "  source files: %d\n", a.SourceFileCount)
	fmt.Fprintf(w, "  p90 dependency amplification: %.1fx\n", a.Metrics.P90DependencyAmplification)
	fmt.Fprintf(w, "  p90 extra deps: %d\n", a.Metrics.P90ExtraDeps)
	if worst := worstFile(a); worst != nil {
		fmt.Fprintf(w, "  worst file: %s\n", worst.Path)
		fmt.Fprintf(w, "    owner rules: %s\n", strings.Join(worst.OwnerRules, ", "))
		fmt.Fprintf(w, "    owner deps: %d\n", worst.Metrics.OwnerDepCount)
		fmt.Fprintf(w, "    package deps: %d\n", worst.Metrics.PackageDepCount)
		fmt.Fprintf(w, "    extra deps: %d\n", worst.Metrics.ExtraDepCount)
	}
	fmt.Fprintln(w)
}

// writeAuditJSON encodes the audit as JSON matching the design document's
// shape, ranked the same way as the text output.
func writeAuditJSON(w io.Writer, audits []*audit.PackageAudit) error {
	ranked := rankAudits(audits)
	report := jsonAuditReport{Packages: make([]jsonAuditPackage, 0, len(ranked))}
	for _, a := range ranked {
		report.Packages = append(report.Packages, toJSONAuditPackage(a))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("encoding audit json: %w", err)
	}
	return nil
}

type jsonAuditReport struct {
	Packages []jsonAuditPackage `json:"packages"`
}

type jsonAuditPackage struct {
	Package     string           `json:"package"`
	Rules       int              `json:"rules"`
	SourceFiles int              `json:"sourceFiles"`
	Metrics     jsonAuditMetrics `json:"metrics"`
	WorstFiles  []jsonAuditFile  `json:"worstFiles"`
	Clusters    []any            `json:"clusters"`
}

type jsonAuditMetrics struct {
	MedianDependencyAmplification float64 `json:"medianDependencyAmplification"`
	P90DependencyAmplification    float64 `json:"p90DependencyAmplification"`
	MaxDependencyAmplification    float64 `json:"maxDependencyAmplification"`
	MedianExtraDeps               int     `json:"medianExtraDeps"`
	P90ExtraDeps                  int     `json:"p90ExtraDeps"`
	MaxExtraDeps                  int     `json:"maxExtraDeps"`
}

type jsonAuditFile struct {
	Path                    string   `json:"path"`
	OwnerRules              []string `json:"ownerRules"`
	OwnerDeps               int      `json:"ownerDeps"`
	PackageDeps             int      `json:"packageDeps"`
	ExtraDeps               int      `json:"extraDeps"`
	DependencyAmplification float64  `json:"dependencyAmplification"`
}

const worstFilesPerPackage = 5

func toJSONAuditPackage(a *audit.PackageAudit) jsonAuditPackage {
	return jsonAuditPackage{
		Package:     a.Package,
		Rules:       a.RuleCount,
		SourceFiles: a.SourceFileCount,
		Metrics: jsonAuditMetrics{
			MedianDependencyAmplification: a.Metrics.MedianDependencyAmplification,
			P90DependencyAmplification:    a.Metrics.P90DependencyAmplification,
			MaxDependencyAmplification:    a.Metrics.MaxDependencyAmplification,
			MedianExtraDeps:               a.Metrics.MedianExtraDeps,
			P90ExtraDeps:                  a.Metrics.P90ExtraDeps,
			MaxExtraDeps:                  a.Metrics.MaxExtraDeps,
		},
		WorstFiles: topWorstFiles(a, worstFilesPerPackage),
		Clusters:   []any{},
	}
}

func topWorstFiles(a *audit.PackageAudit, n int) []jsonAuditFile {
	files := append([]audit.FileInfo(nil), a.Files...)
	sort.SliceStable(files, fileByWorstFirst(files))
	files = files[:min(len(files), n)]
	out := make([]jsonAuditFile, len(files))
	for i, f := range files {
		out[i] = jsonAuditFile{
			Path:                    f.Path,
			OwnerRules:              f.OwnerRules,
			OwnerDeps:               f.Metrics.OwnerDepCount,
			PackageDeps:             f.Metrics.PackageDepCount,
			ExtraDeps:               f.Metrics.ExtraDepCount,
			DependencyAmplification: f.Metrics.DependencyAmplification,
		}
	}
	return out
}

// rankAudits filters out packages with no source files and orders the rest
// by severity score (descending), with package label as a stable tiebreaker.
func rankAudits(audits []*audit.PackageAudit) []*audit.PackageAudit {
	out := make([]*audit.PackageAudit, 0, len(audits))
	for _, a := range audits {
		if a == nil || a.SourceFileCount == 0 {
			continue
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si := scoreAudit(out[i])
		sj := scoreAudit(out[j])
		if si != sj {
			return si > sj
		}
		return out[i].Package < out[j].Package
	})
	return out
}

// scoreAudit ranks packages by p90 amplification weighted by the absolute
// p90 extra-dep count. log1p damps the absolute count so a small package
// with a huge ratio doesn't outrank a large one with broadly distributed
// extra deps.
func scoreAudit(a *audit.PackageAudit) float64 {
	return a.Metrics.P90DependencyAmplification * math.Log1p(float64(a.Metrics.P90ExtraDeps))
}

// worstFile returns the file with the largest extra_dep_count, breaking ties
// by dependency amplification then path. Returns nil for an empty package.
func worstFile(a *audit.PackageAudit) *audit.FileInfo {
	if len(a.Files) == 0 {
		return nil
	}
	worst := &a.Files[0]
	for i := 1; i < len(a.Files); i++ {
		f := &a.Files[i]
		if fileWorse(f, worst) {
			worst = f
		}
	}
	return worst
}

func fileByWorstFirst(files []audit.FileInfo) func(i, j int) bool {
	return func(i, j int) bool {
		return fileWorse(&files[i], &files[j])
	}
}

func fileWorse(a, b *audit.FileInfo) bool {
	if a.Metrics.ExtraDepCount != b.Metrics.ExtraDepCount {
		return a.Metrics.ExtraDepCount > b.Metrics.ExtraDepCount
	}
	if a.Metrics.DependencyAmplification != b.Metrics.DependencyAmplification {
		return a.Metrics.DependencyAmplification > b.Metrics.DependencyAmplification
	}
	return a.Path < b.Path
}
