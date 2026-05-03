package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAuditFlags_Defaults(t *testing.T) {
	cfg, err := parseAuditFlags(nil)
	if err != nil {
		t.Fatalf("parseAuditFlags failed: %v", err)
	}
	if cfg.format != auditFormatText {
		t.Errorf("default format = %q, want %q", cfg.format, auditFormatText)
	}
	if !reflect.DeepEqual(cfg.patterns, []string{"//..."}) {
		t.Errorf("default patterns = %v, want [//...]", cfg.patterns)
	}
}

func TestParseAuditFlags_PositionalPatterns(t *testing.T) {
	cfg, err := parseAuditFlags([]string{"//foo/...", "//bar/..."})
	if err != nil {
		t.Fatalf("parseAuditFlags failed: %v", err)
	}
	want := []string{"//foo/...", "//bar/..."}
	if !reflect.DeepEqual(cfg.patterns, want) {
		t.Errorf("patterns = %v, want %v", cfg.patterns, want)
	}
}

func TestParseAuditFlags_FormatJSON(t *testing.T) {
	cfg, err := parseAuditFlags([]string{"--format=json"})
	if err != nil {
		t.Fatalf("parseAuditFlags failed: %v", err)
	}
	if cfg.format != auditFormatJSON {
		t.Errorf("format = %q, want json", cfg.format)
	}
}

func TestParseAuditFlags_FormatInvalid(t *testing.T) {
	_, err := parseAuditFlags([]string{"--format=yaml"})
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error should mention format: %v", err)
	}
}

func TestParseAuditFlags_UnknownFlagFails(t *testing.T) {
	_, err := parseAuditFlags([]string{"--bogus"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestParseAuditFlags_NoCacheAndDebug(t *testing.T) {
	cfg, err := parseAuditFlags([]string{"--no-cache", "--debug", "--cache-dir=/tmp/x"})
	if err != nil {
		t.Fatalf("parseAuditFlags failed: %v", err)
	}
	if !cfg.noCache || !cfg.debug || cfg.cacheDir != "/tmp/x" {
		t.Errorf("unexpected cfg: %+v", cfg)
	}
}

func TestDepsCacheKey_OrderIndependent(t *testing.T) {
	a := depsCacheKey([]string{"//pkg/foo:a", "//pkg/foo:b"})
	b := depsCacheKey([]string{"//pkg/foo:b", "//pkg/foo:a"})
	if a != b {
		t.Errorf("expected order-independent key, got %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "audit-deps-") {
		t.Errorf("expected key prefix audit-deps-, got %q", a)
	}
	differ := depsCacheKey([]string{"//pkg/foo:c"})
	if differ == a {
		t.Errorf("different inputs produced same key: %q", a)
	}
}
