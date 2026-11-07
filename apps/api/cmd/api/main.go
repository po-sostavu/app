package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/po-sostavu/app/apps/api/internal/platform/config"
	"github.com/po-sostavu/app/apps/api/internal/platform/httpserver"
	"github.com/po-sostavu/app/apps/api/internal/platform/lifecycle"
	"github.com/po-sostavu/app/apps/api/internal/platform/postgres"
)

func main() {
	os.Exit(run())
}

func run() int {
	flags := flag.NewFlagSet("api", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	envFile := flags.String("env-file", "", "explicit local env file (existing environment takes priority)")
	bootstrapLogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stdout)
			flags.PrintDefaults()
			return 0
		}
		bootstrapLogger.Error("Invalid command arguments", "code", "invalid_arguments")
		return 1
	}
	if flags.NArg() != 0 {
		bootstrapLogger.Error("Unexpected command arguments", "code", "invalid_arguments")
		return 1
	}
	if err := config.LoadEnvFile(*envFile); err != nil {
		bootstrapLogger.Error("Configuration failed", "code", "invalid_env_file", "reason", err.Error())
		return 1
	}
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	cfg, err := config.Load(signalCtx)
	if err != nil {
		bootstrapLogger.Error("Configuration failed", "code", "invalid_configuration", "reason", err.Error())
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	db, err := postgres.Open(signalCtx, cfg.Database)
	if err != nil {
		logger.Error("PostgreSQL startup check failed", "code", "database_unavailable")
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Warn("PostgreSQL pool close failed", "code", "database_close_failed")
		}
	}()
	requestCtx, stopRequests := context.WithCancel(context.Background())
	defer stopRequests()
	app := httpserver.New(requestCtx, cfg.HTTP, db, cfg.Database.QueryTimeout, logger)
	if err := lifecycle.Run(signalCtx, app, cfg.HTTP.Addr, cfg.HTTP.ShutdownTimeout, stopRequests, logger); err != nil {
		logger.Error("API stopped with an error", "code", "runtime_failed", "reason", err.Error())
		return 1
	}
	return 0
}
