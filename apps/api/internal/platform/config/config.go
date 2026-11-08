package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/sethvargo/go-envconfig"
)

type Database struct {
	Host            string        `env:"DB_HOST,default=127.0.0.1"`
	Port            uint16        `env:"DB_PORT,default=5432"`
	Name            string        `env:"DB_NAME,default=posostavu"`
	User            string        `env:"DB_USER,default=posostavu_app"`
	Password        string        `env:"DB_PASSWORD,required"`
	SSLMode         string        `env:"DB_SSLMODE,default=verify-full"`
	MaxOpenConns    int           `env:"DB_MAX_OPEN_CONNS,default=10"`
	MaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS,default=5"`
	ConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME,default=5m"`
	ConnectTimeout  time.Duration `env:"DB_CONNECT_TIMEOUT,default=5s"`
	QueryTimeout    time.Duration `env:"DB_QUERY_TIMEOUT,default=2s"`
}

type HTTP struct {
	Addr            string        `env:"HTTP_ADDR,default=127.0.0.1:8081"`
	RequestTimeout  time.Duration `env:"HTTP_REQUEST_TIMEOUT,default=10s"`
	ShutdownTimeout time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT,default=10s"`
	ReadTimeout     time.Duration `env:"HTTP_READ_TIMEOUT,default=5s"`
	WriteTimeout    time.Duration `env:"HTTP_WRITE_TIMEOUT,default=15s"`
	IdleTimeout     time.Duration `env:"HTTP_IDLE_TIMEOUT,default=30s"`
	BodyLimit       int           `env:"HTTP_BODY_LIMIT,default=1048576"`
	Concurrency     int           `env:"HTTP_CONCURRENCY,default=1024"`
}

type Config struct {
	Database Database
	HTTP     HTTP
	LogLevel slog.Level `env:"LOG_LEVEL,default=INFO"`
}

func Load(ctx context.Context) (Config, error) {
	return load(ctx, envconfig.OsLookuper())
}

func LoadDatabase(ctx context.Context) (Database, error) {
	var cfg Database
	if err := decode(ctx, &cfg, envconfig.OsLookuper()); err != nil {
		return Database{}, err
	}
	if err := cfg.validate(); err != nil {
		return Database{}, err
	}
	return cfg, nil
}

// LoadEnvFile only loads an explicitly selected file. Process environment wins.
// It must run before creating goroutines or reading application configuration.
func LoadEnvFile(path string) error {
	if path == "" {
		return nil
	}
	values, err := godotenv.Read(path)
	if err != nil {
		// Parser and filesystem errors may contain the file contents or path.
		return errors.New("cannot read env file: check -env-file and its syntax")
	}
	for key, value := range values {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return errors.New("invalid env file: unsupported key or value")
		}
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return errors.New("cannot load env file")
		}
	}
	return nil
}

func load(ctx context.Context, lookuper envconfig.Lookuper) (Config, error) {
	var cfg Config
	if err := decode(ctx, &cfg, lookuper); err != nil {
		return Config{}, err
	}
	if err := cfg.Database.validate(); err != nil {
		return Config{}, err
	}
	if err := cfg.HTTP.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func decode(ctx context.Context, target any, source envconfig.Lookuper) error {
	// envconfig errors can include the rejected value. Record only the name of
	// the field being decoded and never wrap the original parsing error.
	var key string
	lookuper := envconfig.LookuperFunc(func(name string) (string, bool) {
		key = name
		return source.Lookup(name)
	})
	if err := envconfig.ProcessWith(ctx, &envconfig.Config{Target: target, Lookuper: lookuper}); err != nil {
		return invalid(key)
	}
	return nil
}

func (cfg Database) validate() error {
	for _, field := range []struct{ name, value string }{
		{"DB_HOST", cfg.Host}, {"DB_NAME", cfg.Name}, {"DB_USER", cfg.User},
	} {
		if strings.TrimSpace(field.value) == "" || strings.ContainsAny(field.value, "\x00\r\n") {
			return invalid(field.name)
		}
	}
	if strings.ContainsAny(cfg.Host, "/\\ ,\t") {
		return invalid("DB_HOST")
	}
	if cfg.Port == 0 {
		return invalid("DB_PORT")
	}
	if cfg.Password == "" || strings.ContainsRune(cfg.Password, '\x00') {
		return invalid("DB_PASSWORD")
	}
	// Opportunistic TLS modes silently allow downgrade; local plaintext is
	// explicit, all other connections verify the server certificate and name.
	if cfg.SSLMode != "disable" && cfg.SSLMode != "verify-full" {
		return invalid("DB_SSLMODE")
	}
	if cfg.MaxOpenConns <= 0 {
		return invalid("DB_MAX_OPEN_CONNS")
	}
	if cfg.MaxIdleConns < 0 || cfg.MaxIdleConns > cfg.MaxOpenConns {
		return invalid("DB_MAX_IDLE_CONNS")
	}
	return validateDurations([]durationField{
		{"DB_CONN_MAX_LIFETIME", cfg.ConnMaxLifetime},
		{"DB_CONNECT_TIMEOUT", cfg.ConnectTimeout},
		{"DB_QUERY_TIMEOUT", cfg.QueryTimeout},
	})
}

func (cfg HTTP) validate() error {
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || (host != "" && net.ParseIP(host) == nil && host != "localhost") {
		return invalid("HTTP_ADDR")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return invalid("HTTP_ADDR")
	}
	if cfg.BodyLimit <= 0 {
		return invalid("HTTP_BODY_LIMIT")
	}
	if cfg.Concurrency <= 0 {
		return invalid("HTTP_CONCURRENCY")
	}
	if err := validateDurations([]durationField{
		{"HTTP_REQUEST_TIMEOUT", cfg.RequestTimeout},
		{"HTTP_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout},
		{"HTTP_READ_TIMEOUT", cfg.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", cfg.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", cfg.IdleTimeout},
	}); err != nil {
		return err
	}
	if cfg.WriteTimeout <= cfg.RequestTimeout {
		return invalid("HTTP_WRITE_TIMEOUT")
	}
	return nil
}

type durationField struct {
	name  string
	value time.Duration
}

func validateDurations(fields []durationField) error {
	for _, field := range fields {
		if field.value <= 0 {
			return invalid(field.name)
		}
	}
	return nil
}

func invalid(key string) error {
	return fmt.Errorf("invalid configuration: %s", key)
}
