package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestInvalidArgumentsDoNotEchoValues(t *testing.T) {
	var output bytes.Buffer
	if code := run([]string{"-timeout", "private-marker", "up"}, io.Discard, &output); code != 2 {
		t.Fatalf("invalid arguments exit code = %d", code)
	}
	if strings.Contains(output.String(), "private-marker") {
		t.Fatal("invalid argument value was exposed")
	}
}

func TestCreateMigrationRejectsTraversalAndPreservesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := createMigration(dir, "../outside", io.Discard); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := createMigration(dir, "first", io.Discard); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dir, "000001_first.up.sql")
	if err := os.WriteFile(first, []byte("original SQL"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := createMigration(dir, "next", io.Discard); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(first)
	if err != nil || string(content) != "original SQL" {
		t.Fatalf("existing migration modified: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "000002_next.down.sql")); err != nil {
		t.Fatal(err)
	}
}

func TestRejectIncompleteOrAmbiguousMigrationSet(t *testing.T) {
	for name, files := range map[string][]string{
		"missing down":      {"000001_first.up.sql"},
		"duplicate version": {"000001_first.up.sql", "000001_first.down.sql", "000001_second.up.sql", "000001_second.down.sql"},
		"unrecognized SQL":  {"init.sql"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, filename := range files {
				if err := os.WriteFile(filepath.Join(dir, filename), []byte("SELECT 1;"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := inspectMigrations(dir); err == nil {
				t.Fatal("invalid migration set accepted")
			}
		})
	}
}

func TestDestructiveCommandsRequireExplicitConfirmation(t *testing.T) {
	for _, args := range [][]string{{"down-all"}, {"force", "1"}, {"-confirm", "force", "-2"}} {
		if _, err := parseOptions(args, io.Discard); err == nil {
			t.Fatalf("unsafe command accepted: %v", args)
		}
	}
	for _, args := range [][]string{{"down"}, {"-confirm", "down-all"}, {"-confirm", "force", "1"}} {
		if _, err := parseOptions(args, io.Discard); err != nil {
			t.Fatalf("valid command rejected: %v", err)
		}
	}
}

func TestMigrationErrorDoesNotExposeSQLOrRowData(t *testing.T) {
	err := &pgconn.PgError{Code: "23505", Message: "secret-password", Detail: "private-profile", InternalQuery: "private SQL"}
	message := safeError(err)
	if !strings.Contains(message, "23505") {
		t.Fatal("safe diagnostic SQLSTATE missing")
	}
	for _, secret := range []string{err.Message, err.Detail, err.InternalQuery} {
		if strings.Contains(message, secret) {
			t.Fatal("sensitive PostgreSQL error escaped")
		}
	}
}
