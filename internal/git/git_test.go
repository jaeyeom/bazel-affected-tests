package git

import (
	"context"
	"errors"
	"testing"

	executor "github.com/jaeyeom/go-cmdexec"
)

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
			errContains: "failed to get staged files",
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
