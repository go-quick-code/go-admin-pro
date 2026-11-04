package service

import (
	"testing"
	"time"
)

func TestHasPermission(t *testing.T) {
	cases := []struct {
		name     string
		codes    []string
		required string
		want     bool
	}{
		{"exact", []string{"system:user:query"}, "system:user:query", true},
		{"missing", []string{"system:user:query"}, "system:user:create", false},
		{"super admin", []string{"*"}, "any:thing:here", true},
		{"wildcard prefix", []string{"system:*"}, "system:user:query", true},
		{"wildcard mismatch", []string{"system:*"}, "infra:file:query", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasPermission(tc.codes, tc.required); got != tc.want {
				t.Fatalf("HasPermission(%v, %q) = %v, want %v", tc.codes, tc.required, got, tc.want)
			}
		})
	}
}

func TestIsSuperAdmin(t *testing.T) {
	if !IsSuperAdmin([]string{"common", "super_admin"}) {
		t.Fatal("expected super_admin to be detected")
	}
	if IsSuperAdmin([]string{"common"}) {
		t.Fatal("unexpected super_admin")
	}
}

func TestParseExpireTime(t *testing.T) {
	if got := parseExpireTime(""); got != nil {
		t.Fatalf("empty value should return nil, got %v", got)
	}
	got := parseExpireTime("2030-01-02 03:04:05")
	if got == nil || got.Year() != 2030 {
		t.Fatalf("unexpected parse result: %v", got)
	}
	got = parseExpireTime("2031-02-03T04:05:06+08:00")
	if got == nil || got.Year() != 2031 {
		t.Fatalf("unexpected RFC3339 parse result: %v", got)
	}
	if _, err := time.Parse("2006-01-02", "2030-12-31"); err != nil {
		t.Fatal(err)
	}
}
