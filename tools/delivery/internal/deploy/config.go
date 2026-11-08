package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/po-sostavu/app/tools/delivery/internal/release"
)

const Root = "/opt/po-sostavu"

type Config struct {
	Enabled          bool   `json:"enabled"`
	Mode             string `json:"mode"`
	RecoveryVerified bool   `json:"recovery_verified"`
	Domain           string `json:"domain"`
	EdgeNetwork      string `json:"edge_network"`
	TraefikDirectory string `json:"traefik_directory"`
	HTTPEntrypoint   string `json:"http_entrypoint"`
	HTTPSEntrypoint  string `json:"https_entrypoint"`
	CertResolver     string `json:"cert_resolver"`
	ObserveSeconds   int    `json:"observe_seconds"`
	ReserveMemoryMiB int    `json:"reserve_memory_mib"`
	ReserveDiskMiB   int    `json:"reserve_disk_mib"`
}

func (config Config) validateMode() error {
	if !config.Enabled {
		return errors.New("deployment not enabled")
	}
	switch config.Mode {
	case "", "production":
		if !config.RecoveryVerified {
			return errors.New("production recovery not verified")
		}
	case "test":
	default:
		return errors.New("unknown deployment mode")
	}
	return nil
}

func LoadConfig(root string) (Config, error) {
	var config Config
	files := filepath.Join(root, "deploy")
	for _, name := range []string{"config.json", "compose.database.yaml", "compose.application.yaml", "Caddyfile", "init.sh"} {
		if err := Trusted(filepath.Join(files, name), false); err != nil {
			return config, err
		}
	}
	values := map[string]map[string]string{}
	for _, name := range []string{"postgres", "api", "migrate"} {
		file := filepath.Join(root, "secrets", name+".env")
		if err := Trusted(file, true); err != nil {
			return config, err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return config, err
		}
		entry := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok || len(value) < 24 || strings.ContainsAny(value, "\r\x00") || entry[key] != "" {
				return config, errors.New("invalid database secret")
			}
			entry[key] = value
		}
		expected := []string{"DB_PASSWORD"}
		if name == "postgres" {
			expected = []string{"POSTGRES_PASSWORD", "APP_DB_PASSWORD", "MIGRATOR_DB_PASSWORD"}
		}
		if len(entry) != len(expected) {
			return config, errors.New("unexpected secret field")
		}
		for _, key := range expected {
			if entry[key] == "" {
				return config, errors.New("missing database secret")
			}
		}
		values[name] = entry
	}
	passwords := values["postgres"]
	if passwords["POSTGRES_PASSWORD"] == passwords["APP_DB_PASSWORD"] || passwords["POSTGRES_PASSWORD"] == passwords["MIGRATOR_DB_PASSWORD"] || passwords["APP_DB_PASSWORD"] == passwords["MIGRATOR_DB_PASSWORD"] {
		return config, errors.New("database roles need different passwords")
	}
	if values["api"]["DB_PASSWORD"] != passwords["APP_DB_PASSWORD"] || values["migrate"]["DB_PASSWORD"] != passwords["MIGRATOR_DB_PASSWORD"] {
		return config, errors.New("database secret mismatch")
	}
	raw, err := os.ReadFile(filepath.Join(files, "config.json"))
	if err != nil {
		return config, err
	}
	if err := release.StrictJSON(raw, &config); err != nil {
		return config, err
	}
	if err := config.validateMode(); err != nil {
		return config, err
	}
	if !regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`).MatchString(config.Domain) {
		return config, errors.New("invalid domain")
	}
	for _, name := range []string{config.EdgeNetwork, config.HTTPEntrypoint, config.HTTPSEntrypoint, config.CertResolver} {
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) || strings.HasPrefix(name, "REPLACE") {
			return config, errors.New("configure Traefik names first")
		}
	}
	if config.ObserveSeconds < 30 || config.ObserveSeconds > 600 || config.ReserveMemoryMiB < 128 || config.ReserveMemoryMiB > 8192 || config.ReserveDiskMiB < 1024 || config.ReserveDiskMiB > 102400 {
		return config, errors.New("invalid resource or observation limits")
	}
	if err := Trusted(config.TraefikDirectory, false); err != nil {
		return config, err
	}
	for _, name := range []string{filepath.Join(root, "assets"), filepath.Join(root, "backups"), filepath.Join(files, "docker-client"), filepath.Join(files, "state.json"), filepath.Join(files, "in-progress.json"), filepath.Join(files, "deployment.lock"), filepath.Join(config.TraefikDirectory, "posostavu.yml")} {
		if Exists(name) {
			if err := Trusted(name, false); err != nil {
				return config, err
			}
		}
	}
	return config, nil
}
