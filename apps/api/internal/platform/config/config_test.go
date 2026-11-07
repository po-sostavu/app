package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sethvargo/go-envconfig"
)

func TestConfigurationRejectsUnsafeValuesWithoutEchoingThem(t *testing.T) {
	t.Parallel()
	const secret = "private-value-must-not-appear"
	for _, test := range []struct {
		name, key, value string
	}{
		{"missing password", "DB_PASSWORD", ""},
		{"invalid port", "DB_PORT", secret},
		{"zero port", "DB_PORT", "0"},
		{"invalid timeout", "DB_CONNECT_TIMEOUT", secret},
		{"unbounded query", "DB_QUERY_TIMEOUT", "0"},
		{"unbounded pool", "DB_MAX_OPEN_CONNS", "0"},
		{"oversized idle pool", "DB_MAX_IDLE_CONNS", "11"},
		{"TLS downgrade", "DB_SSLMODE", "prefer"},
		{"unverified TLS", "DB_SSLMODE", "require"},
		{"invalid log level", "LOG_LEVEL", secret},
		{"invalid bind address", "HTTP_ADDR", secret},
		{"unbounded request", "HTTP_REQUEST_TIMEOUT", "0"},
		{"unbounded shutdown", "HTTP_SHUTDOWN_TIMEOUT", "0"},
		{"unbounded transport", "HTTP_READ_TIMEOUT", "0"},
		{"transport shorter than request", "HTTP_WRITE_TIMEOUT", "1s"},
		{"unbounded body", "HTTP_BODY_LIMIT", "0"},
		{"unbounded concurrency", "HTTP_CONCURRENCY", "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := map[string]string{"DB_PASSWORD": secret}
			values[test.key] = test.value
			_, err := load(context.Background(), envconfig.MapLookuper(values))
			if err == nil {
				t.Fatal("invalid configuration was accepted")
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Fatalf("error does not name %s", test.key)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("configuration error exposes the supplied value")
			}
		})
	}
}

func TestEnvFileIsExplicitAndPreservesProcessEnvironment(t *testing.T) {
	const key = "POSOSTAVU_TEST_ENV_FILE_PRIORITY"
	t.Setenv(key, "process-value")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(key+"=file-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(key) != "process-value" {
		t.Fatal("env file replaced the existing process environment")
	}
	if err := LoadEnvFile(""); err != nil {
		t.Fatal("an unspecified env file must not be read")
	}
}

func TestMalformedEnvFileDoesNotExposeItsContents(t *testing.T) {
	const secret = "private-value-must-not-appear"
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("invalid key ! "+secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := LoadEnvFile(path)
	if err == nil {
		t.Fatal("malformed env file was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("env parser error exposes file contents")
	}
}
