package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jaeyeom/bazel-affected-tests/internal/audit"
)

func sampleAudit(pkg string, p90Amp float64, p90Extra int, files []audit.FileInfo) *audit.PackageAudit {
	a := &audit.PackageAudit{
		Package:         pkg,
		RuleCount:       len(files),
		SourceFileCount: len(files),
		Files:           files,
		Metrics: audit.PackageMetrics{
			P90DependencyAmplification: p90Amp,
			P90ExtraDeps:               p90Extra,
		},
	}
	return a
}

func sampleFile(path string, ownerDeps, pkgDeps, extra int) audit.FileInfo {
	return audit.FileInfo{
		Path:       path,
		OwnerRules: []string{"//pkg:lib"},
		Metrics: audit.FileMetrics{
			OwnerDepCount:           ownerDeps,
			PackageDepCount:         pkgDeps,
			ExtraDepCount:           extra,
			DependencyAmplification: float64(pkgDeps) / float64(ownerDeps),
		},
	}
}

func TestRankAudits_OrdersBySeverityAndFiltersEmpty(t *testing.T) {
	low := sampleAudit("//low", 1.5, 5, []audit.FileInfo{sampleFile("low/a", 2, 3, 1)})
	high := sampleAudit("//high", 10.0, 200, []audit.FileInfo{sampleFile("high/a", 2, 20, 18)})
	mid := sampleAudit("//mid", 5.0, 50, []audit.FileInfo{sampleFile("mid/a", 2, 10, 8)})
	empty := sampleAudit("//empty", 0, 0, nil)

	got := rankAudits([]*audit.PackageAudit{low, empty, high, mid})
	wantOrder := []string{"//high", "//mid", "//low"}
	gotOrder := make([]string, len(got))
	for i, a := range got {
		gotOrder[i] = a.Package
	}
	for i, w := range wantOrder {
		if i >= len(gotOrder) || gotOrder[i] != w {
			t.Errorf("rank order = %v, want %v", gotOrder, wantOrder)
			break
		}
	}
	if len(got) != 3 {
		t.Errorf("expected //empty to be filtered out, got %d packages", len(got))
	}
}

func TestWorstFile_PicksLargestExtraDeps(t *testing.T) {
	a := sampleAudit("//pkg", 0, 0, []audit.FileInfo{
		sampleFile("a", 5, 10, 5),
		sampleFile("b", 2, 10, 8),
		sampleFile("c", 3, 10, 7),
	})
	worst := worstFile(a)
	if worst == nil || worst.Path != "b" {
		t.Errorf("worst file = %+v, want b", worst)
	}
}

func TestWriteAuditText_RankedOutput(t *testing.T) {
	high := sampleAudit("//high", 10.0, 200, []audit.FileInfo{
		sampleFile("high/a.go", 2, 20, 18),
		sampleFile("high/b.go", 5, 20, 15),
	})
	low := sampleAudit("//low", 1.5, 2, []audit.FileInfo{
		sampleFile("low/a.go", 3, 4, 1),
	})

	var buf bytes.Buffer
	writeAuditText(&buf, []*audit.PackageAudit{low, high})
	out := buf.String()

	if !strings.Contains(out, "Package cohesion audit") {
		t.Errorf("missing header in output:\n%s", out)
	}
	highIdx := strings.Index(out, "//high")
	lowIdx := strings.Index(out, "//low")
	if highIdx < 0 || lowIdx < 0 {
		t.Fatalf("expected both packages in output:\n%s", out)
	}
	if highIdx > lowIdx {
		t.Errorf("//high should appear before //low (higher severity):\n%s", out)
	}
	// Worst-file block expected for the high-severity package.
	if !strings.Contains(out, "worst file: high/a.go") {
		t.Errorf("expected worst file high/a.go in output:\n%s", out)
	}
	if !strings.Contains(out, "extra deps: 18") {
		t.Errorf("expected extra deps line for worst file:\n%s", out)
	}
}

func TestWriteAuditText_EmptyAudits(t *testing.T) {
	var buf bytes.Buffer
	writeAuditText(&buf, nil)
	if !strings.Contains(buf.String(), "no packages with source files") {
		t.Errorf("expected empty-result message, got: %q", buf.String())
	}
}

func TestWriteAuditJSON_ShapeAndOrder(t *testing.T) {
	high := sampleAudit("//high", 10.0, 200, []audit.FileInfo{
		sampleFile("high/a.go", 2, 20, 18),
		sampleFile("high/b.go", 5, 20, 15),
	})
	high.Metrics.MedianDependencyAmplification = 5.0
	high.Metrics.MaxDependencyAmplification = 10.0
	high.Metrics.MaxExtraDeps = 18

	low := sampleAudit("//low", 1.5, 2, []audit.FileInfo{
		sampleFile("low/a.go", 3, 4, 1),
	})

	var buf bytes.Buffer
	if err := writeAuditJSON(&buf, []*audit.PackageAudit{low, high}); err != nil {
		t.Fatalf("writeAuditJSON failed: %v", err)
	}

	var got jsonAuditReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, buf.String())
	}
	if len(got.Packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(got.Packages))
	}
	// Severity ordering preserved: //high before //low.
	if got.Packages[0].Package != "//high" || got.Packages[1].Package != "//low" {
		t.Errorf("package order = %s, %s; want //high, //low",
			got.Packages[0].Package, got.Packages[1].Package)
	}
	first := got.Packages[0]
	if first.Metrics.P90DependencyAmplification != 10.0 {
		t.Errorf("p90 amp = %v, want 10.0", first.Metrics.P90DependencyAmplification)
	}
	if first.Metrics.P90ExtraDeps != 200 {
		t.Errorf("p90 extra = %d, want 200", first.Metrics.P90ExtraDeps)
	}
	if len(first.WorstFiles) != 2 {
		t.Errorf("worst_files len = %d, want 2", len(first.WorstFiles))
	}
	// Largest extra_deps first.
	if first.WorstFiles[0].Path != "high/a.go" {
		t.Errorf("first worst file = %q, want high/a.go", first.WorstFiles[0].Path)
	}
	// Clusters slot exists for forward compatibility but is empty in Phase 1.
	if first.Clusters == nil {
		t.Errorf("clusters should be an empty array, got nil")
	}
}

func TestTopWorstFiles_RespectsLimit(t *testing.T) {
	a := sampleAudit("//pkg", 0, 0, []audit.FileInfo{
		sampleFile("a", 2, 10, 8),
		sampleFile("b", 2, 10, 7),
		sampleFile("c", 2, 10, 6),
		sampleFile("d", 2, 10, 5),
	})
	got := topWorstFiles(a, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 files, got %d", len(got))
	}
	if got[0].ExtraDeps != 8 || got[1].ExtraDeps != 7 {
		t.Errorf("top files extras = %d, %d; want 8, 7", got[0].ExtraDeps, got[1].ExtraDeps)
	}
}
