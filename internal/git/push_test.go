package git

import (
	"context"
	"errors"
	"strings"
	"testing"

	executor "github.com/jaeyeom/go-cmdexec"
)

func TestIsZeroOID(t *testing.T) {
	tests := []struct {
		oid  string
		want bool
	}{
		{ZeroOID, true},
		{"0000000000000000000000000000000000000000", true},
		{"000", true},
		{"abc", false},
		{"000a", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsZeroOID(tt.oid); got != tt.want {
			t.Errorf("IsZeroOID(%q) = %v, want %v", tt.oid, got, tt.want)
		}
	}
}

func TestParsePushUpdates(t *testing.T) {
	input := strings.Join([]string{
		"refs/heads/feature abc111 refs/heads/feature def222",
		"",
		"refs/heads/other 012345 refs/heads/other " + ZeroOID,
	}, "\n")

	got, err := ParsePushUpdates(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParsePushUpdates() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParsePushUpdates() len = %d, want 2", len(got))
	}
	if got[0].LocalRef != "refs/heads/feature" || got[0].LocalOID != "abc111" {
		t.Errorf("first update = %+v", got[0])
	}
	if got[1].RemoteOID != ZeroOID {
		t.Errorf("second RemoteOID = %q, want zero", got[1].RemoteOID)
	}
}

func TestParsePushUpdates_Malformed(t *testing.T) {
	_, err := ParsePushUpdates(strings.NewReader("not enough fields\n"))
	if err == nil {
		t.Fatal("ParsePushUpdates() expected error for malformed line")
	}
}

func TestParseDiffSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		wantOld string
		wantNew string
		wantDot bool
		wantErr bool
	}{
		{"two-dot", "abc..def", "abc", "def", false, false},
		{"three-dot", "origin/main...HEAD", "origin/main", "HEAD", true, false},
		{"empty", "", "", "", false, true},
		{"no separator", "abc", "", "", false, true},
		{"empty old", "..def", "", "", false, true},
		{"empty new", "abc..", "", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRev, newRev, three, err := ParseDiffSpec(tt.spec)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if oldRev != tt.wantOld || newRev != tt.wantNew || three != tt.wantDot {
				t.Errorf("ParseDiffSpec(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.spec, oldRev, newRev, three, tt.wantOld, tt.wantNew, tt.wantDot)
			}
		})
	}
}

func TestFilesFromPushUpdates_PRMode(t *testing.T) {
	localOID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	remoteOID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	mb := "cccccccccccccccccccccccccccccccccccccccc"

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "merge-base", "origin/main", localOID).
		WillSucceed(mb+"\n", 0).
		Build()
	mockExec.ExpectCommandWithArgs("git", "diff", mb, localOID, "--name-only", "--diff-filter=ACM").
		WillSucceed("pkg/a.go\npkg/b.go", 0).
		Build()

	updates := []PushUpdate{{
		LocalRef:  "refs/heads/feature",
		LocalOID:  localOID,
		RemoteRef: "refs/heads/feature",
		RemoteOID: remoteOID,
	}}

	got, err := FilesFromPushUpdates(context.Background(), mockExec, updates, "origin/main", PushModePR)
	if err != nil {
		t.Fatalf("FilesFromPushUpdates() error: %v", err)
	}
	want := []string{"pkg/a.go", "pkg/b.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilesFromPushUpdates_TipMode(t *testing.T) {
	localOID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	remoteOID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "diff", remoteOID, localOID, "--name-only", "--diff-filter=ACM").
		WillSucceed("only/changed.go", 0).
		Build()

	updates := []PushUpdate{{
		LocalRef:  "refs/heads/feature",
		LocalOID:  localOID,
		RemoteRef: "refs/heads/feature",
		RemoteOID: remoteOID,
	}}

	got, err := FilesFromPushUpdates(context.Background(), mockExec, updates, "origin/main", PushModeTip)
	if err != nil {
		t.Fatalf("FilesFromPushUpdates() error: %v", err)
	}
	if len(got) != 1 || got[0] != "only/changed.go" {
		t.Errorf("got %v, want [only/changed.go]", got)
	}
}

func TestFilesFromPushUpdates_TipModeNewBranchFallsBackToPR(t *testing.T) {
	localOID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mb := "cccccccccccccccccccccccccccccccccccccccc"

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "merge-base", "origin/main", localOID).
		WillSucceed(mb+"\n", 0).
		Build()
	mockExec.ExpectCommandWithArgs("git", "diff", mb, localOID, "--name-only", "--diff-filter=ACM").
		WillSucceed("new/branch.go", 0).
		Build()

	updates := []PushUpdate{{
		LocalRef:  "refs/heads/feature",
		LocalOID:  localOID,
		RemoteRef: "refs/heads/feature",
		RemoteOID: ZeroOID,
	}}

	got, err := FilesFromPushUpdates(context.Background(), mockExec, updates, "origin/main", PushModeTip)
	if err != nil {
		t.Fatalf("FilesFromPushUpdates() error: %v", err)
	}
	if len(got) != 1 || got[0] != "new/branch.go" {
		t.Errorf("got %v, want [new/branch.go]", got)
	}
}

func TestFilesFromPushUpdates_SkipsDeletesAndTags(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	// No git commands expected — only delete and tag updates.

	updates := []PushUpdate{
		{
			LocalRef:  "refs/heads/gone",
			LocalOID:  ZeroOID,
			RemoteRef: "refs/heads/gone",
			RemoteOID: "dddddddddddddddddddddddddddddddddddddddd",
		},
		{
			LocalRef:  "refs/tags/v1.0.0",
			LocalOID:  "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			RemoteRef: "refs/tags/v1.0.0",
			RemoteOID: ZeroOID,
		},
	}

	got, err := FilesFromPushUpdates(context.Background(), mockExec, updates, "origin/main", PushModePR)
	if err != nil {
		t.Fatalf("FilesFromPushUpdates() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestFilesFromPushUpdates_DedupesAcrossRefs(t *testing.T) {
	local1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	local2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	mb1 := "cccccccccccccccccccccccccccccccccccccccc"
	mb2 := "dddddddddddddddddddddddddddddddddddddddd"

	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "merge-base", "origin/main", local1).
		WillSucceed(mb1+"\n", 0).
		Build()
	mockExec.ExpectCommandWithArgs("git", "diff", mb1, local1, "--name-only", "--diff-filter=ACM").
		WillSucceed("shared.go\na_only.go", 0).
		Build()
	mockExec.ExpectCommandWithArgs("git", "merge-base", "origin/main", local2).
		WillSucceed(mb2+"\n", 0).
		Build()
	mockExec.ExpectCommandWithArgs("git", "diff", mb2, local2, "--name-only", "--diff-filter=ACM").
		WillSucceed("shared.go\nb_only.go", 0).
		Build()

	updates := []PushUpdate{
		{LocalRef: "refs/heads/a", LocalOID: local1, RemoteRef: "refs/heads/a", RemoteOID: ZeroOID},
		{LocalRef: "refs/heads/b", LocalOID: local2, RemoteRef: "refs/heads/b", RemoteOID: ZeroOID},
	}

	got, err := FilesFromPushUpdates(context.Background(), mockExec, updates, "origin/main", PushModePR)
	if err != nil {
		t.Fatalf("FilesFromPushUpdates() error: %v", err)
	}
	// sorted unique
	want := []string{"a_only.go", "b_only.go", "shared.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilesFromPushUpdates_MergeBaseError(t *testing.T) {
	localOID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "merge-base", "origin/main", localOID).
		WillError(errors.New("no common ancestor")).
		Build()

	updates := []PushUpdate{{
		LocalRef:  "refs/heads/feature",
		LocalOID:  localOID,
		RemoteRef: "refs/heads/feature",
		RemoteOID: ZeroOID,
	}}

	_, err := FilesFromPushUpdates(context.Background(), mockExec, updates, "origin/main", PushModePR)
	if err == nil {
		t.Fatal("expected merge-base error")
	}
}
