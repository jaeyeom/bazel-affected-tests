package git

import (
	"context"
	"errors"
	"testing"

	executor "github.com/jaeyeom/go-cmdexec"
)

func TestRepoRoot(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(m *executor.MockExecutor)
		want        string
		wantErr     bool
		errContains string
	}{
		{
			name: "returns trimmed repo root",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "rev-parse", "--show-toplevel").
					WillSucceed("/home/user/repo\n", 0).
					Build()
			},
			want: "/home/user/repo",
		},
		{
			name: "error when not a git repo",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "rev-parse", "--show-toplevel").
					WillError(errors.New("not a git repository")).
					Build()
			},
			wantErr:     true,
			errContains: "failed to find git repo root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockExec := executor.NewMockExecutor()
			tt.setupMock(mockExec)

			got, err := RepoRoot(context.Background(), mockExec)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("RepoRoot() expected error, got nil")
				}
				if tt.errContains != "" && !containsStr(err.Error(), tt.errContains) {
					t.Errorf("RepoRoot() error = %q, want it to contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("RepoRoot() unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("RepoRoot() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetStagedFiles(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(m *executor.MockExecutor)
		want        []string
		wantErr     bool
		errContains string
	}{
		{
			name: "empty output returns empty slice",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "--cached", "--name-only", "--diff-filter=ACM").
					WillSucceed("", 0).
					Build()
			},
			want:    []string{},
			wantErr: false,
		},
		{
			name: "single file staged",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "--cached", "--name-only", "--diff-filter=ACM").
					WillSucceed("internal/git/git.go", 0).
					Build()
			},
			want:    []string{"internal/git/git.go"},
			wantErr: false,
		},
		{
			name: "multiple files staged",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "--cached", "--name-only", "--diff-filter=ACM").
					WillSucceed("internal/git/git.go\ncmd/main.go\ninternal/query/bazel.go", 0).
					Build()
			},
			want:    []string{"internal/git/git.go", "cmd/main.go", "internal/query/bazel.go"},
			wantErr: false,
		},
		{
			name: "output with trailing newline",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "--cached", "--name-only", "--diff-filter=ACM").
					WillSucceed("internal/git/git.go\ncmd/main.go\n", 0).
					Build()
			},
			want:    []string{"internal/git/git.go", "cmd/main.go"},
			wantErr: false,
		},
		{
			name: "output with empty lines filters them out",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "--cached", "--name-only", "--diff-filter=ACM").
					WillSucceed("internal/git/git.go\n\ncmd/main.go\n\n", 0).
					Build()
			},
			want:    []string{"internal/git/git.go", "cmd/main.go"},
			wantErr: false,
		},
		{
			name: "executor error returns error",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "--cached", "--name-only", "--diff-filter=ACM").
					WillError(errors.New("connection refused")).
					Build()
			},
			want:        nil,
			wantErr:     true,
			errContains: "failed to get diff files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockExec := executor.NewMockExecutor()
			tt.setupMock(mockExec)

			got, err := GetStagedFiles(context.Background(), mockExec)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("GetStagedFiles() expected error, got nil")
				}
				if tt.errContains != "" && !containsStr(err.Error(), tt.errContains) {
					t.Errorf("GetStagedFiles() error = %q, want it to contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("GetStagedFiles() unexpected error: %v", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("GetStagedFiles() returned %d files, want %d: got %v, want %v",
					len(got), len(tt.want), got, tt.want)
			}

			for i, f := range got {
				if f != tt.want[i] {
					t.Errorf("GetStagedFiles()[%d] = %q, want %q", i, f, tt.want[i])
				}
			}
		})
	}
}

func TestGetHeadFiles(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(m *executor.MockExecutor)
		want      []string
		wantErr   bool
	}{
		{
			name: "returns staged and unstaged files",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "HEAD", "--name-only", "--diff-filter=ACM").
					WillSucceed("file1.go\nfile2.go", 0).
					Build()
			},
			want: []string{"file1.go", "file2.go"},
		},
		{
			name: "empty output returns empty slice",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "HEAD", "--name-only", "--diff-filter=ACM").
					WillSucceed("", 0).
					Build()
			},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockExec := executor.NewMockExecutor()
			tt.setupMock(mockExec)

			got, err := GetHeadFiles(context.Background(), mockExec)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, f := range got {
				if f != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, f, tt.want[i])
				}
			}
		})
	}
}

func TestGetDiffFiles(t *testing.T) {
	tests := []struct {
		name      string
		ref       string
		setupMock func(m *executor.MockExecutor)
		want      []string
		wantErr   bool
	}{
		{
			name: "diff against main",
			ref:  "main",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "main", "--name-only", "--diff-filter=ACM").
					WillSucceed("pkg/a.go\npkg/b.go", 0).
					Build()
			},
			want: []string{"pkg/a.go", "pkg/b.go"},
		},
		{
			name: "diff against commit SHA",
			ref:  "abc123",
			setupMock: func(m *executor.MockExecutor) {
				m.ExpectCommandWithArgs("git", "diff", "abc123", "--name-only", "--diff-filter=ACM").
					WillSucceed("changed.go", 0).
					Build()
			},
			want: []string{"changed.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockExec := executor.NewMockExecutor()
			tt.setupMock(mockExec)

			got, err := GetDiffFiles(context.Background(), mockExec, tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, f := range got {
				if f != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, f, tt.want[i])
				}
			}
		})
	}
}

func TestGetDiffBetween(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "diff", "abc", "def", "--name-only", "--diff-filter=ACM").
		WillSucceed("a.go\nb.go", 0).
		Build()

	got, err := GetDiffBetween(context.Background(), mockExec, "abc", "def")
	if err != nil {
		t.Fatalf("GetDiffBetween() error: %v", err)
	}
	want := []string{"a.go", "b.go"}
	if len(got) != len(want) {
		t.Fatalf("GetDiffBetween() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GetDiffBetween()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGetDiffRange(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "diff", "origin/main...HEAD", "--name-only", "--diff-filter=ACM").
		WillSucceed("pkg/x.go", 0).
		Build()

	got, err := GetDiffRange(context.Background(), mockExec, "origin/main...HEAD")
	if err != nil {
		t.Fatalf("GetDiffRange() error: %v", err)
	}
	if len(got) != 1 || got[0] != "pkg/x.go" {
		t.Errorf("GetDiffRange() = %v, want [pkg/x.go]", got)
	}
}

func TestMergeBase(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "merge-base", "origin/main", "abc123").
		WillSucceed("baseoid\n", 0).
		Build()

	got, err := MergeBase(context.Background(), mockExec, "origin/main", "abc123")
	if err != nil {
		t.Fatalf("MergeBase() error: %v", err)
	}
	if got != "baseoid" {
		t.Errorf("MergeBase() = %q, want baseoid", got)
	}
}

func TestMergeBase_Error(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	mockExec.ExpectCommandWithArgs("git", "merge-base", "a", "b").
		WillError(errors.New("no merge base")).
		Build()

	_, err := MergeBase(context.Background(), mockExec, "a", "b")
	if err == nil {
		t.Fatal("MergeBase() expected error, got nil")
	}
}

func TestResolveDefaultBase(t *testing.T) {
	t.Run("preferred exists", func(t *testing.T) {
		mockExec := executor.NewMockExecutor()
		mockExec.ExpectCommandWithArgs("git", "rev-parse", "--verify", "--quiet", "upstream/develop").
			WillSucceed("abc\n", 0).
			Build()

		got, err := ResolveDefaultBase(context.Background(), mockExec, "upstream/develop")
		if err != nil {
			t.Fatalf("ResolveDefaultBase() error: %v", err)
		}
		if got != "upstream/develop" {
			t.Errorf("ResolveDefaultBase() = %q, want upstream/develop", got)
		}
	})

	t.Run("falls back to origin/main", func(t *testing.T) {
		mockExec := executor.NewMockExecutor()
		// preferred missing
		mockExec.ExpectCommandWithArgs("git", "rev-parse", "--verify", "--quiet", "missing").
			WillError(errors.New("missing")).
			Build()
		mockExec.ExpectCommandWithArgs("git", "rev-parse", "--verify", "--quiet", "origin/main").
			WillSucceed("abc\n", 0).
			Build()

		got, err := ResolveDefaultBase(context.Background(), mockExec, "missing")
		if err != nil {
			t.Fatalf("ResolveDefaultBase() error: %v", err)
		}
		if got != "origin/main" {
			t.Errorf("ResolveDefaultBase() = %q, want origin/main", got)
		}
	})

	t.Run("none found", func(t *testing.T) {
		mockExec := executor.NewMockExecutor()
		for _, c := range []string{"origin/main", "main", "origin/master", "master"} {
			mockExec.ExpectCommandWithArgs("git", "rev-parse", "--verify", "--quiet", c).
				WillError(errors.New("missing")).
				Build()
		}

		_, err := ResolveDefaultBase(context.Background(), mockExec, "")
		if err == nil {
			t.Fatal("ResolveDefaultBase() expected error when no candidate exists")
		}
	})
}

func containsStr(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
