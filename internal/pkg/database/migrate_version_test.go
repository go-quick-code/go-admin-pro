package database

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestCheckMigrationVersion(t *testing.T) {
	tests := []struct {
		name     string
		applied  []int64
		expected []int64
		wantKind MigrationVersionMismatchKind
		wantErr  string
	}{
		{
			name:     "up to date",
			applied:  []int64{1, 2},
			expected: []int64{1, 2},
		},
		{
			name:     "empty database is behind",
			applied:  nil,
			expected: []int64{1},
			wantKind: MigrationBehind,
			wantErr:  "database is not migrated",
		},
		{
			name:     "missing latest migration is behind",
			applied:  []int64{1},
			expected: []int64{1, 2},
			wantKind: MigrationBehind,
			wantErr:  "applied version 1, binary expects 2",
		},
		{
			name:     "unknown later migration is ahead",
			applied:  []int64{1, 2},
			expected: []int64{1},
			wantKind: MigrationAhead,
			wantErr:  "applied version 2, binary only knows 1",
		},
		{
			name:     "non-prefix history diverged",
			applied:  []int64{1, 3},
			expected: []int64{1, 2},
			wantKind: MigrationDiverged,
			wantErr:  "diverged",
		},
		{
			name:     "empty binary migrations fail closed",
			applied:  nil,
			expected: nil,
			wantErr:  "binary embeds no migrations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMigrationVersion(tt.applied, tt.expected)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
			var mismatch *MigrationVersionError
			if !errors.As(err, &mismatch) {
				if tt.wantKind != "" {
					t.Fatalf("expected *MigrationVersionError, got %T", err)
				}
				return
			}
			if mismatch.Kind != tt.wantKind {
				t.Fatalf("kind = %q, want %q", mismatch.Kind, tt.wantKind)
			}
		})
	}
}

func TestEmbeddedVersions(t *testing.T) {
	versions, err := embeddedVersions()
	if err != nil {
		t.Fatalf("embeddedVersions: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("expected at least one embedded migration")
	}
	if !slices.IsSorted(versions) {
		t.Fatalf("versions not sorted: %v", versions)
	}
	if versions[0] != 1 {
		t.Fatalf("first migration version = %d, want 1", versions[0])
	}
}
