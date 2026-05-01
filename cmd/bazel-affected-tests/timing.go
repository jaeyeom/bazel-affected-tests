package main

import (
	"fmt"
	"io"
	"time"
)

// stageTimer collects per-stage wall-clock durations for --timing output.
// A nil or disabled timer is a no-op, so callers can wrap stages
// unconditionally.
type stageTimer struct {
	enabled bool
	started time.Time
	entries []stageEntry
}

type stageEntry struct {
	name     string
	duration time.Duration
}

func newStageTimer(enabled bool) *stageTimer {
	return &stageTimer{enabled: enabled, started: time.Now()}
}

// stage returns a stop function that records the elapsed time when called.
func (s *stageTimer) stage(name string) func() {
	if s == nil || !s.enabled {
		return func() {}
	}
	start := time.Now()
	return func() {
		s.entries = append(s.entries, stageEntry{name: name, duration: time.Since(start)})
	}
}

func (s *stageTimer) report(w io.Writer) {
	if s == nil || !s.enabled {
		return
	}
	nameWidth := len("total")
	for _, e := range s.entries {
		if len(e.name) > nameWidth {
			nameWidth = len(e.name)
		}
	}

	total := time.Since(s.started)
	rounded := make([]string, len(s.entries))
	durWidth := 0
	for i, e := range s.entries {
		rounded[i] = e.duration.Round(time.Microsecond).String()
		if len(rounded[i]) > durWidth {
			durWidth = len(rounded[i])
		}
	}
	totalStr := total.Round(time.Microsecond).String()
	if len(totalStr) > durWidth {
		durWidth = len(totalStr)
	}

	fmt.Fprintln(w, "timing:")
	for i, e := range s.entries {
		pct := 0.0
		if total > 0 {
			pct = float64(e.duration) / float64(total) * 100
		}
		fmt.Fprintf(w, "  %-*s  %*s  (%5.1f%%)\n", nameWidth, e.name, durWidth, rounded[i], pct)
	}
	fmt.Fprintf(w, "  %-*s  %*s\n", nameWidth, "total", durWidth, totalStr)
}
