package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestStageTimer_DisabledIsNoop(t *testing.T) {
	timer := newStageTimer(false)
	stop := timer.stage("anything")
	stop()

	var buf bytes.Buffer
	timer.report(&buf)
	if buf.Len() != 0 {
		t.Errorf("expected disabled timer to produce no output, got %q", buf.String())
	}
}

func TestStageTimer_NilIsNoop(t *testing.T) {
	var timer *stageTimer
	stop := timer.stage("anything") // must not panic
	stop()

	var buf bytes.Buffer
	timer.report(&buf)
	if buf.Len() != 0 {
		t.Errorf("expected nil timer to produce no output, got %q", buf.String())
	}
}

func TestStageTimer_ReportContainsStagesAndTotal(t *testing.T) {
	timer := newStageTimer(true)
	stop := timer.stage("alpha")
	stop()
	stop = timer.stage("beta")
	stop()

	var buf bytes.Buffer
	timer.report(&buf)
	out := buf.String()

	if !strings.HasPrefix(out, "timing:\n") {
		t.Errorf("expected report to start with 'timing:' header, got %q", out)
	}
	for _, want := range []string{"alpha", "beta", "total"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected report to contain %q, got %q", want, out)
		}
	}
}

func TestStageTimer_ReportContainsPercentages(t *testing.T) {
	timer := newStageTimer(true)
	stop := timer.stage("alpha")
	stop()

	var buf bytes.Buffer
	timer.report(&buf)
	out := buf.String()

	if !strings.Contains(out, "%") {
		t.Errorf("expected percent sign in stage line, got %q", out)
	}
	// The total line itself should not have a percentage (it's the denominator).
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if strings.Contains(line, "total") && strings.Contains(line, "%") {
			t.Errorf("total line should not contain a percentage: %q", line)
		}
	}
}

func TestStageTimer_ReportPreservesStageOrder(t *testing.T) {
	timer := newStageTimer(true)
	for _, name := range []string{"first", "second", "third"} {
		stop := timer.stage(name)
		stop()
	}

	var buf bytes.Buffer
	timer.report(&buf)
	out := buf.String()

	first := strings.Index(out, "first")
	second := strings.Index(out, "second")
	third := strings.Index(out, "third")
	if first < 0 || second < 0 || third < 0 {
		t.Fatalf("missing stage in report: %q", out)
	}
	if first >= second || second >= third {
		t.Errorf("expected stages in insertion order, got %q", out)
	}
}
