package query

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	executor "github.com/jaeyeom/go-cmdexec"
)

const sampleXML = `<?xml version="1.1" encoding="UTF-8" standalone="no"?>
<query version="2">
    <rule class="go_library" location="pkg/foo/BUILD.bazel:1" name="//pkg/foo:foo_lib">
        <string name="name" value="foo_lib"/>
        <list name="srcs">
            <label value="//pkg/foo:foo.go"/>
            <label value="//pkg/foo:bar.go"/>
        </list>
        <list name="deps">
            <label value="//pkg/dep:lib"/>
            <label value="//pkg/other:lib"/>
        </list>
        <list name="data">
            <label value="//pkg/foo:testdata.txt"/>
        </list>
        <list name="tags">
            <string value="manual"/>
        </list>
    </rule>
    <rule class="go_test" location="pkg/foo/BUILD.bazel:20" name="//pkg/foo:foo_test">
        <list name="srcs">
            <label value="//pkg/foo:foo_test.go"/>
        </list>
        <list name="deps">
            <label value="//pkg/foo:foo_lib"/>
        </list>
    </rule>
</query>
`

func TestParseRulesXML_ExtractsKnownAttrs(t *testing.T) {
	rules, err := parseRulesXML([]byte(sampleXML))
	if err != nil {
		t.Fatalf("parseRulesXML failed: %v", err)
	}

	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	lib := rules[0]
	if lib.Kind != "go_library" {
		t.Errorf("rules[0].Kind = %q, want go_library", lib.Kind)
	}
	if lib.Label != "//pkg/foo:foo_lib" {
		t.Errorf("rules[0].Label = %q, want //pkg/foo:foo_lib", lib.Label)
	}
	wantSrcs := []string{"//pkg/foo:foo.go", "//pkg/foo:bar.go"}
	if !reflect.DeepEqual(lib.Sources["srcs"], wantSrcs) {
		t.Errorf("rules[0].Sources[srcs] = %v, want %v", lib.Sources["srcs"], wantSrcs)
	}
	wantData := []string{"//pkg/foo:testdata.txt"}
	if !reflect.DeepEqual(lib.Sources["data"], wantData) {
		t.Errorf("rules[0].Sources[data] = %v, want %v", lib.Sources["data"], wantData)
	}
	wantDeps := []string{"//pkg/dep:lib", "//pkg/other:lib"}
	if !reflect.DeepEqual(lib.Deps["deps"], wantDeps) {
		t.Errorf("rules[0].Deps[deps] = %v, want %v", lib.Deps["deps"], wantDeps)
	}

	// "tags" is not in SourceAttrs or DepAttrs and must be dropped.
	if _, ok := lib.Sources["tags"]; ok {
		t.Errorf("rules[0].Sources[tags] should not be populated")
	}
	if _, ok := lib.Deps["tags"]; ok {
		t.Errorf("rules[0].Deps[tags] should not be populated")
	}
}

func TestParseRulesXML_EmptyDoc(t *testing.T) {
	rules, err := parseRulesXML([]byte(`<query version="2"></query>`))
	if err != nil {
		t.Fatalf("parseRulesXML failed: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseRulesXML_RuleWithNoLists(t *testing.T) {
	doc := `<query version="2">
		<rule class="filegroup" name="//pkg:fg"/>
	</query>`
	rules, err := parseRulesXML([]byte(doc))
	if err != nil {
		t.Fatalf("parseRulesXML failed: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Sources != nil {
		t.Errorf("Sources should be nil when no source attrs present, got %v", rules[0].Sources)
	}
	if rules[0].Deps != nil {
		t.Errorf("Deps should be nil when no dep attrs present, got %v", rules[0].Deps)
	}
}

func TestParseRulesXML_Malformed(t *testing.T) {
	_, err := parseRulesXML([]byte("<query><rule"))
	if err == nil {
		t.Fatal("expected error for malformed XML")
	}
	if !strings.Contains(err.Error(), "parsing bazel xml") {
		t.Errorf("error should mention 'parsing bazel xml', got: %v", err)
	}
}

func TestQueryRules_Success(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	q := NewBazelQuerierWithExecutor(mockExec)

	mockExec.ExpectCommandWithArgs("bazel", "query", "--output=xml", "//pkg/foo:*").
		WillSucceed(sampleXML, 0).
		Once().
		Build()

	rules, err := q.QueryRules("//pkg/foo:*")
	if err != nil {
		t.Fatalf("QueryRules failed: %v", err)
	}
	if len(rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(rules))
	}
	if err := mockExec.AssertExpectationsMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}

func TestQueryRules_EmptyResult(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	q := NewBazelQuerierWithExecutor(mockExec)

	mockExec.ExpectCommandWithArgs("bazel", "query", "--output=xml", "//empty:*").
		WillSucceed("", 0).
		Build()

	rules, err := q.QueryRules("//empty:*")
	if err != nil {
		t.Fatalf("QueryRules failed: %v", err)
	}
	if rules != nil {
		t.Errorf("expected nil rules for empty result, got %v", rules)
	}
}

func TestQueryRules_QueryError(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	q := NewBazelQuerierWithExecutor(mockExec)

	mockExec.ExpectCommandWithArgs("bazel", "query", "--output=xml", "//bad:*").
		WillFail("ERROR: invalid pattern", 2).
		Build()

	_, err := q.QueryRules("//bad:*")
	if err == nil {
		t.Fatal("expected error from QueryRules when bazel query fails")
	}
	if !strings.Contains(err.Error(), "querying rules for //bad:*") {
		t.Errorf("error should be wrapped with rule context, got: %v", err)
	}
}

func TestQueryDeps_SingleTarget(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	q := NewBazelQuerierWithExecutor(mockExec)

	mockExec.ExpectCommandWithArgs("bazel", "query", "deps(//pkg/foo:*)").
		WillSucceed("//pkg/foo:foo_lib\n//pkg/dep:lib\n//pkg/other:lib", 0).
		Once().
		Build()

	deps, err := q.QueryDeps([]string{"//pkg/foo:*"})
	if err != nil {
		t.Fatalf("QueryDeps failed: %v", err)
	}

	want := []string{"//pkg/dep:lib", "//pkg/foo:foo_lib", "//pkg/other:lib"}
	got := append([]string(nil), deps...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestQueryDeps_MultipleTargetsUseSet(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	q := NewBazelQuerierWithExecutor(mockExec)

	mockExec.ExpectCommandWithArgs("bazel", "query", "deps(set(//pkg/foo:a //pkg/foo:b))").
		WillSucceed("//pkg/foo:a\n//pkg/foo:b\n//pkg/dep:lib", 0).
		Once().
		Build()

	deps, err := q.QueryDeps([]string{"//pkg/foo:a", "//pkg/foo:b"})
	if err != nil {
		t.Fatalf("QueryDeps failed: %v", err)
	}
	if len(deps) != 3 {
		t.Errorf("expected 3 deps, got %d: %v", len(deps), deps)
	}
	if err := mockExec.AssertExpectationsMet(); err != nil {
		t.Errorf("mock expectations not met: %v", err)
	}
}

func TestQueryPackages_FiltersExternalAndPrefixes(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	q := NewBazelQuerierWithExecutor(mockExec)

	mockExec.ExpectCommandWithArgs("bazel", "query", "--output=package", "//foo/...").
		WillSucceed("foo\nfoo/bar\n@external//foo\nfoo/baz", 0).
		Once().
		Build()

	pkgs, err := q.QueryPackages("//foo/...")
	if err != nil {
		t.Fatalf("QueryPackages failed: %v", err)
	}
	want := []string{"//foo", "//foo/bar", "//foo/baz"}
	if !reflect.DeepEqual(pkgs, want) {
		t.Errorf("packages = %v, want %v", pkgs, want)
	}
}

func TestQueryPackages_EmptyResult(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	q := NewBazelQuerierWithExecutor(mockExec)

	mockExec.ExpectCommandWithArgs("bazel", "query", "--output=package", "//empty/...").
		WillSucceed("", 0).
		Build()

	pkgs, err := q.QueryPackages("//empty/...")
	if err != nil {
		t.Fatalf("QueryPackages failed: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("expected no packages, got %v", pkgs)
	}
}

func TestQueryDeps_EmptyTargets(t *testing.T) {
	mockExec := executor.NewMockExecutor()
	q := NewBazelQuerierWithExecutor(mockExec)

	deps, err := q.QueryDeps(nil)
	if err != nil {
		t.Fatalf("QueryDeps with nil should not error: %v", err)
	}
	if deps != nil {
		t.Errorf("expected nil deps for empty input, got %v", deps)
	}

	if len(mockExec.GetCallHistory()) != 0 {
		t.Errorf("expected no executor calls for empty input")
	}
}
