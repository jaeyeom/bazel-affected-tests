package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantConfig *Config
		wantErr    bool
	}{
		{
			name: "valid config with rules",
			content: `version: 1
rules:
  - patterns:
      - "**/BUILD"
      - "**/BUILD.bazel"
    targets:
      - "//..."
  - patterns:
      - "**/*.bzl"
    targets:
      - "//tools/..."
`,
			wantConfig: &Config{
				Version: 1,
				Rules: []Rule{
					{
						Patterns: []string{"**/BUILD", "**/BUILD.bazel"},
						Targets:  []string{"//..."},
					},
					{
						Patterns: []string{"**/*.bzl"},
						Targets:  []string{"//tools/..."},
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "invalid YAML",
			content: "invalid: [yaml: content",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			// Write config file
			if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := LoadConfig(tmpDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !reflect.DeepEqual(got, tt.wantConfig) {
				t.Errorf("LoadConfig() = %+v, want %+v", got, tt.wantConfig)
			}
		})
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()

	got, err := LoadConfig(tmpDir)
	if err != nil {
		t.Errorf("LoadConfig() error = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("LoadConfig() = %v, want nil", got)
	}
}

func TestConfig_MatchTargets(t *testing.T) {
	config := &Config{
		Version: 1,
		Rules: []Rule{
			{
				Patterns: []string{"**/BUILD", "**/BUILD.bazel"},
				Targets:  []string{"//..."},
			},
			{
				Patterns: []string{"**/*.bzl"},
				Targets:  []string{"//tools/...", "//build/..."},
			},
			{
				Patterns: []string{"WORKSPACE"},
				Targets:  []string{"//..."},
			},
		},
	}

	tests := []struct {
		name  string
		files []string
		want  []string
	}{
		{
			name:  "BUILD file matches first rule",
			files: []string{"foo/bar/BUILD"},
			want:  []string{"//..."},
		},
		{
			name:  ".bzl file matches second rule",
			files: []string{"tools/defs.bzl"},
			want:  []string{"//tools/...", "//build/..."},
		},
		{
			name:  "multiple files match different rules",
			files: []string{"foo/BUILD", "bar/defs.bzl"},
			want:  []string{"//...", "//tools/...", "//build/..."},
		},
		{
			name:  "WORKSPACE matches third rule",
			files: []string{"WORKSPACE"},
			want:  []string{"//..."},
		},
		{
			name:  "no matching files",
			files: []string{"README.md", "foo.txt"},
			want:  []string{},
		},
		{
			name:  "empty files list",
			files: []string{},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.MatchTargets(tt.files)
			sort.Strings(got)
			sort.Strings(tt.want)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MatchTargets() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadConfig_UnsupportedVersion(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"version 0 (unset) accepted", "rules: []\n", false},
		{"version 1 accepted", "version: 1\n", false},
		{"version 2 rejected", "version: 2\n", true},
		{"version 999 rejected", "version: 999\n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := LoadConfig(tmpDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadConfig_InvalidPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory instead of a file
	if err := os.Mkdir(filepath.Join(tmpDir, ConfigFileName), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(tmpDir)
	if err == nil {
		t.Error("LoadConfig() expected error when config is a directory, got nil")
	}
}

func TestConfig_ShouldExclude(t *testing.T) {
	config := &Config{
		Version: 1,
		Exclude: []string{"//tools/format:*"},
	}

	tests := []struct {
		target string
		want   bool
	}{
		{"//tools/format:format_test_Go_with_gofmt", true},
		{"//tools/format:format_test_Python_with_ruff", true},
		{"//pkg/foo:foo_test", false},
		{"//tools/lint:lint_test", false},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			if got := config.ShouldExclude(tt.target); got != tt.want {
				t.Errorf("ShouldExclude(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestConfig_FilterExcluded(t *testing.T) {
	config := &Config{
		Version: 1,
		Exclude: []string{"//tools/format:*"},
	}

	input := []string{
		"//pkg/foo:foo_test",
		"//tools/format:format_test_Go_with_gofmt",
		"//tools/format:format_test_Python_with_ruff",
		"//pkg/bar:bar_test",
	}
	got := config.FilterExcluded(input)
	want := []string{"//pkg/foo:foo_test", "//pkg/bar:bar_test"}

	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterExcluded() = %v, want %v", got, want)
	}
}

func TestConfig_FilterExcluded_NoExcludes(t *testing.T) {
	config := &Config{Version: 1}
	input := []string{"//pkg/foo:test", "//tools/format:test"}
	got := config.FilterExcluded(input)
	if !reflect.DeepEqual(got, input) {
		t.Errorf("FilterExcluded() = %v, want %v (unchanged)", got, input)
	}
}

func TestConfig_FilterIgnoredFiles(t *testing.T) {
	config := &Config{
		Version: 1,
		IgnorePaths: []string{
			".semgrep/**",
			"docs/**",
			"*.md",
		},
	}

	tests := []struct {
		name  string
		files []string
		want  []string
	}{
		{
			name:  "filters semgrep files",
			files: []string{".semgrep/py3-logging-format.yaml", "src/main.go"},
			want:  []string{"src/main.go"},
		},
		{
			name:  "filters docs files",
			files: []string{"docs/guide.md", "docs/api/reference.html", "src/lib.go"},
			want:  []string{"src/lib.go"},
		},
		{
			name:  "filters root markdown files",
			files: []string{"README.md", "CHANGELOG.md", "src/main.go"},
			want:  []string{"src/main.go"},
		},
		{
			name:  "all files filtered",
			files: []string{".semgrep/rule.yaml", "docs/readme.md"},
			want:  nil,
		},
		{
			name:  "no files filtered",
			files: []string{"src/main.go", "pkg/lib/lib.go"},
			want:  []string{"src/main.go", "pkg/lib/lib.go"},
		},
		{
			name:  "empty files list",
			files: []string{},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.FilterIgnoredFiles(tt.files)
			// Normalize nil/empty for comparison
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FilterIgnoredFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_FilterIgnoredFiles_NoIgnorePaths(t *testing.T) {
	config := &Config{Version: 1}
	input := []string{"src/main.go", ".semgrep/rule.yaml"}
	got := config.FilterIgnoredFiles(input)
	if !reflect.DeepEqual(got, input) {
		t.Errorf("FilterIgnoredFiles() = %v, want %v (unchanged)", got, input)
	}
}

func TestLoadConfig_WithIgnorePaths(t *testing.T) {
	tmpDir := t.TempDir()
	content := `version: 1
ignore_paths:
  - ".semgrep/**"
  - "docs/**"
  - "*.md"
`
	if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	want := &Config{
		Version:     1,
		IgnorePaths: []string{".semgrep/**", "docs/**", "*.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadConfig() = %+v, want %+v", got, want)
	}
}

func TestConfig_SubpackageQueryEnabled(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name   string
		config *Config
		want   bool
	}{
		{
			name:   "nil pointer defaults to true",
			config: &Config{Version: 1},
			want:   true,
		},
		{
			name:   "explicitly true",
			config: &Config{Version: 1, EnableSubpackageQuery: boolPtr(true)},
			want:   true,
		},
		{
			name:   "explicitly false",
			config: &Config{Version: 1, EnableSubpackageQuery: boolPtr(false)},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.SubpackageQueryEnabled(); got != tt.want {
				t.Errorf("SubpackageQueryEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadConfig_WithEnableSubpackageQuery(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name    string
		content string
		want    *bool
	}{
		{
			name:    "not set",
			content: "version: 1\n",
			want:    nil,
		},
		{
			name:    "set to false",
			content: "version: 1\nenable_subpackage_query: false\n",
			want:    boolPtr(false),
		},
		{
			name:    "set to true",
			content: "version: 1\nenable_subpackage_query: true\n",
			want:    boolPtr(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := LoadConfig(tmpDir)
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}

			if tt.want == nil {
				if got.EnableSubpackageQuery != nil {
					t.Errorf("EnableSubpackageQuery = %v, want nil", *got.EnableSubpackageQuery)
				}
			} else {
				if got.EnableSubpackageQuery == nil {
					t.Errorf("EnableSubpackageQuery = nil, want %v", *tt.want)
				} else if *got.EnableSubpackageQuery != *tt.want {
					t.Errorf("EnableSubpackageQuery = %v, want %v", *got.EnableSubpackageQuery, *tt.want)
				}
			}
		})
	}
}

func TestConfig_ResolvedMaxParentDepth(t *testing.T) {
	intPtr := func(i int) *int { return &i }

	tests := []struct {
		name     string
		config   *Config
		fallback int
		want     int
	}{
		{"nil config returns fallback", nil, 1, 1},
		{"nil field returns fallback", &Config{Version: 1}, 1, 1},
		{"explicit 0 overrides fallback", &Config{Version: 1, MaxParentDepth: intPtr(0)}, 1, 0},
		{"explicit 3 overrides fallback", &Config{Version: 1, MaxParentDepth: intPtr(3)}, 1, 3},
		{"explicit -1 (unlimited) overrides fallback", &Config{Version: 1, MaxParentDepth: intPtr(-1)}, 1, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.ResolvedMaxParentDepth(tt.fallback); got != tt.want {
				t.Errorf("ResolvedMaxParentDepth(%d) = %d, want %d", tt.fallback, got, tt.want)
			}
		})
	}
}

func TestLoadConfig_WithMaxParentDepthAndStrict(t *testing.T) {
	intPtr := func(i int) *int { return &i }

	tests := []struct {
		name       string
		content    string
		wantMaxPtr *int
		wantStrict bool
	}{
		{
			name:       "not set",
			content:    "version: 1\n",
			wantMaxPtr: nil,
			wantStrict: false,
		},
		{
			name:       "max_parent_depth 0",
			content:    "version: 1\nmax_parent_depth: 0\n",
			wantMaxPtr: intPtr(0),
			wantStrict: false,
		},
		{
			name:       "max_parent_depth -1 (unlimited)",
			content:    "version: 1\nmax_parent_depth: -1\n",
			wantMaxPtr: intPtr(-1),
			wantStrict: false,
		},
		{
			name:       "strict true",
			content:    "version: 1\nstrict: true\n",
			wantMaxPtr: nil,
			wantStrict: true,
		},
		{
			name:       "both set",
			content:    "version: 1\nmax_parent_depth: 2\nstrict: true\n",
			wantMaxPtr: intPtr(2),
			wantStrict: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmpDir, ConfigFileName), []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := LoadConfig(tmpDir)
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}

			if tt.wantMaxPtr == nil {
				if got.MaxParentDepth != nil {
					t.Errorf("MaxParentDepth = %v, want nil", *got.MaxParentDepth)
				}
			} else {
				if got.MaxParentDepth == nil {
					t.Errorf("MaxParentDepth = nil, want %v", *tt.wantMaxPtr)
				} else if *got.MaxParentDepth != *tt.wantMaxPtr {
					t.Errorf("MaxParentDepth = %v, want %v", *got.MaxParentDepth, *tt.wantMaxPtr)
				}
			}
			if got.Strict != tt.wantStrict {
				t.Errorf("Strict = %v, want %v", got.Strict, tt.wantStrict)
			}
		})
	}
}

func TestConfig_MatchTargets_Deduplication(t *testing.T) {
	config := &Config{
		Version: 1,
		Rules: []Rule{
			{
				Patterns: []string{"**/BUILD"},
				Targets:  []string{"//..."},
			},
			{
				Patterns: []string{"**/BUILD.bazel"},
				Targets:  []string{"//..."},
			},
		},
	}

	files := []string{"foo/BUILD", "bar/BUILD.bazel"}
	got := config.MatchTargets(files)

	if len(got) != 1 {
		t.Errorf("MatchTargets() returned %d targets, want 1 (deduplicated)", len(got))
	}

	if len(got) > 0 && got[0] != "//..." {
		t.Errorf("MatchTargets() = %v, want [//...]", got)
	}
}
