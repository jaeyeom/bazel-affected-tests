package query

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

// SourceAttrs lists the rule attributes treated as source-like for the audit's
// file-ownership analysis. The set is intentionally conservative: treating
// every label-valued attribute as ownership produces noisy results.
var SourceAttrs = []string{"srcs", "hdrs", "data", "resources"}

// DepAttrs lists the rule attributes treated as dependency edges.
var DepAttrs = []string{"deps", "runtime_deps", "exports", "implementation_deps"}

// Rule is the structured form of a single Bazel rule extracted from
// `bazel query --output=xml`. Sources and Deps are keyed by attribute name
// and hold the labels Bazel reports for that attribute. Only attribute names
// in SourceAttrs / DepAttrs are populated.
type Rule struct {
	Kind    string
	Label   string
	Sources map[string][]string
	Deps    map[string][]string
}

// QueryRules runs `bazel query <pattern> --output=xml` and returns parsed
// rule metadata. Returns nil for an empty result.
func (q *BazelQuerier) QueryRules(pattern string) ([]Rule, error) {
	raw, err := q.queryRaw(pattern, "--output=xml")
	if err != nil {
		return nil, fmt.Errorf("querying rules for %s: %w", pattern, err)
	}
	if raw == "" {
		return nil, nil
	}
	return parseRulesXML([]byte(raw))
}

// QueryPackages lists the unique workspace packages matched by pattern using
// `bazel query <pattern> --output=package`. External-repo packages
// (those containing "@") are filtered out so the audit stays scoped to the
// local workspace.
func (q *BazelQuerier) QueryPackages(pattern string) ([]string, error) {
	lines, err := q.query(pattern, "--output=package")
	if err != nil {
		return nil, fmt.Errorf("listing packages for %s: %w", pattern, err)
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "@") {
			continue
		}
		out = append(out, "//"+l)
	}
	return out, nil
}

// QueryDeps returns the dependency closure for the given targets via
// `bazel query 'deps(...)'`. A single target uses `deps(target)`; multiple
// targets are wrapped as `deps(set(t1 t2 ...))`. Empty input returns nil.
func (q *BazelQuerier) QueryDeps(targets []string) ([]string, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	var expr string
	if len(targets) == 1 {
		expr = fmt.Sprintf("deps(%s)", targets[0])
	} else {
		expr = fmt.Sprintf("deps(set(%s))", strings.Join(targets, " "))
	}
	return q.query(expr)
}

// queryXML mirrors `bazel query --output=xml`:
//
//	<query>
//	  <rule class="..." name="...">
//	    <list name="srcs"><label value="..."/></list>
//	    ...
//	  </rule>
//	</query>
type queryXML struct {
	XMLName xml.Name  `xml:"query"`
	Rules   []ruleXML `xml:"rule"`
}

type ruleXML struct {
	Class string    `xml:"class,attr"`
	Name  string    `xml:"name,attr"`
	Lists []listXML `xml:"list"`
}

type listXML struct {
	Name   string     `xml:"name,attr"`
	Labels []labelXML `xml:"label"`
}

type labelXML struct {
	Value string `xml:"value,attr"`
}

func parseRulesXML(data []byte) ([]Rule, error) {
	// Bazel emits XML 1.1, which Go's encoding/xml rejects. The body is
	// 1.0-compatible, so dropping the declaration is safe.
	var doc queryXML
	if err := xml.Unmarshal(stripXMLDeclaration(data), &doc); err != nil {
		return nil, fmt.Errorf("parsing bazel xml: %w", err)
	}
	sources := stringSet(SourceAttrs)
	deps := stringSet(DepAttrs)
	rules := make([]Rule, 0, len(doc.Rules))
	for _, r := range doc.Rules {
		rule := Rule{Kind: r.Class, Label: r.Name}
		for _, l := range r.Lists {
			switch {
			case sources[l.Name]:
				if rule.Sources == nil {
					rule.Sources = make(map[string][]string)
				}
				rule.Sources[l.Name] = collectLabels(l.Labels)
			case deps[l.Name]:
				if rule.Deps == nil {
					rule.Deps = make(map[string][]string)
				}
				rule.Deps[l.Name] = collectLabels(l.Labels)
			}
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func collectLabels(labels []labelXML) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l.Value != "" {
			out = append(out, l.Value)
		}
	}
	return out
}

// stripXMLDeclaration drops the leading <?xml ... ?> processing instruction
// from data so a Bazel-emitted XML 1.1 document can be parsed by Go's
// encoding/xml (which only supports XML 1.0). Bazel's XML body itself stays
// within the 1.0 grammar.
func stripXMLDeclaration(data []byte) []byte {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("<?xml")) {
		return data
	}
	_, after, found := bytes.Cut(trimmed, []byte("?>"))
	if !found {
		return data
	}
	return after
}

func stringSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}
