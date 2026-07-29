package git

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	executor "github.com/jaeyeom/go-cmdexec"
)

// ZeroOID is the all-zero object name git hooks use for missing/new/deleted refs.
const ZeroOID = "0000000000000000000000000000000000000000"

// IsZeroOID reports whether oid is the all-zero object name (any length of zeros
// matching git's convention for new/deleted refs in hook protocols).
func IsZeroOID(oid string) bool {
	if oid == "" {
		return false
	}
	for _, c := range oid {
		if c != '0' {
			return false
		}
	}
	return true
}

// PushUpdate is one ref update supplied on pre-push hook stdin.
//
//	<local ref> <local oid> <remote ref> <remote oid>
type PushUpdate struct {
	LocalRef  string
	LocalOID  string
	RemoteRef string
	RemoteOID string
}

// PushMode selects how FilesFromPushUpdates computes the changed-file set.
type PushMode int

const (
	// PushModePR diffs each local tip against the merge-base with the default
	// integration branch (recommended for rebase force-push / PR scope).
	PushModePR PushMode = iota
	// PushModeTip diffs remote_oid..local_oid (tip-to-tip). New branches fall
	// back to PR-scope against the default base.
	PushModeTip
)

// ParsePushUpdates reads git pre-push stdin lines into PushUpdate values.
// Blank lines are ignored. Malformed non-empty lines return an error.
func ParsePushUpdates(r io.Reader) ([]PushUpdate, error) {
	var updates []PushUpdate
	sc := bufio.NewScanner(r)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("pre-push stdin line %d: want 4 fields, got %d: %q", lineNum, len(fields), line)
		}
		updates = append(updates, PushUpdate{
			LocalRef:  fields[0],
			LocalOID:  fields[1],
			RemoteRef: fields[2],
			RemoteOID: fields[3],
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading pre-push stdin: %w", err)
	}
	return updates, nil
}

// FilesFromPushUpdates returns the union of changed files for the given push
// updates. Branch deletes are skipped. Non-branch refs (tags, notes, …) are
// skipped. Results are sorted and deduplicated.
//
// defaultBase is the integration branch used for PR-scope diffs (e.g.
// origin/main). It must resolve when any update needs PR-scope (PushModePR, or
// PushModeTip with a new branch).
func FilesFromPushUpdates(ctx context.Context, exec executor.Executor, updates []PushUpdate, defaultBase string, mode PushMode) ([]string, error) {
	seen := make(map[string]bool)
	var files []string

	for _, u := range updates {
		if IsZeroOID(u.LocalOID) {
			// Branch delete — nothing to test.
			continue
		}
		if !strings.HasPrefix(u.LocalRef, "refs/heads/") {
			continue
		}

		var part []string
		var err error
		switch {
		case mode == PushModeTip && !IsZeroOID(u.RemoteOID):
			part, err = GetDiffBetween(ctx, exec, u.RemoteOID, u.LocalOID)
		default:
			// PR-scope, or tip mode on a new branch: merge-base(defaultBase, local) → local.
			if defaultBase == "" {
				return nil, fmt.Errorf("default base branch is required for PR-scope push file discovery")
			}
			mb, mbErr := MergeBase(ctx, exec, defaultBase, u.LocalOID)
			if mbErr != nil {
				return nil, fmt.Errorf("merge-base %s %s (ref %s): %w", defaultBase, u.LocalOID, u.LocalRef, mbErr)
			}
			part, err = GetDiffBetween(ctx, exec, mb, u.LocalOID)
		}
		if err != nil {
			return nil, fmt.Errorf("diff for %s: %w", u.LocalRef, err)
		}
		for _, f := range part {
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}

	sort.Strings(files)
	return files, nil
}

// ParseDiffSpec parses a --diff value of the form "old..new" or "old...new".
// Both sides must be non-empty. threeDot is true for the "..." form.
func ParseDiffSpec(spec string) (oldRev, newRev string, threeDot bool, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", false, fmt.Errorf("empty --diff range")
	}
	if i := strings.Index(spec, "..."); i >= 0 {
		oldRev, newRev = spec[:i], spec[i+3:]
		threeDot = true
	} else if i := strings.Index(spec, ".."); i >= 0 {
		oldRev, newRev = spec[:i], spec[i+2:]
	} else {
		return "", "", false, fmt.Errorf("invalid --diff range %q (want old..new or old...new)", spec)
	}
	if oldRev == "" || newRev == "" {
		return "", "", false, fmt.Errorf("invalid --diff range %q: both sides must be non-empty", spec)
	}
	return oldRev, newRev, threeDot, nil
}
