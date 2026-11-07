// Command migrate manages schema changes independently of the HTTP process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/po-sostavu/app/apps/api/internal/platform/config"
	"github.com/po-sostavu/app/apps/api/internal/platform/postgres"
)

type options struct {
	envFile string
	path    string
	timeout time.Duration
	confirm bool
	command string
	arg     string
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	opts, err := parseOptions(args, errOut)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "Invalid migration command. Run with -help.")
		return 2
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, opts.timeout)
	defer cancel()

	// The driver has context-free metadata operations. A process-level deadline
	// bounds those as well; exit closes connections and an unfinished migration
	// may remain dirty. Never automatically force its version after interruption.
	result := make(chan error, 1)
	go func() { result <- execute(ctx, opts, out) }()
	select {
	case err = <-result:
		if err != nil {
			_, _ = fmt.Fprintln(errOut, safeError(err))
			return 1
		}
		return 0
	case <-ctx.Done():
		_, _ = fmt.Fprintln(errOut, "Migration interrupted or timed out; check database version and dirty state before retrying.")
		return 1
	}
}

func parseOptions(args []string, out io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.envFile, "env-file", "", "explicit local environment file (existing environment wins)")
	flags.StringVar(&opts.path, "path", "migrations", "directory containing paired SQL migrations")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "total operation deadline (1s to 10m)")
	flags.BoolVar(&opts.confirm, "confirm", false, "acknowledge destructive down-all or manual force")
	flags.Usage = func() {
		flags.SetOutput(out)
		_, _ = fmt.Fprintln(out, "Usage: migrate [flags] create <name> | up | down | version | down-all | force <version>")
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	if opts.timeout < time.Second || opts.timeout > 10*time.Minute || flags.NArg() < 1 {
		return opts, errors.New("invalid arguments")
	}
	opts.command = flags.Arg(0)
	opts.arg = flags.Arg(1)
	wantArgs := 1
	switch opts.command {
	case "create":
		wantArgs = 2
		if !namePattern.MatchString(opts.arg) {
			return opts, errors.New("invalid migration name")
		}
	case "force":
		wantArgs = 2
		version, err := strconv.Atoi(opts.arg)
		if err != nil || version < -1 || !opts.confirm {
			return opts, errors.New("force requires an explicit version and -confirm")
		}
	case "down-all":
		if !opts.confirm {
			return opts, errors.New("down-all requires -confirm")
		}
	case "up", "down", "version":
	default:
		return opts, errors.New("unknown command")
	}
	if flags.NArg() != wantArgs {
		return opts, errors.New("invalid argument count")
	}
	return opts, nil
}

func execute(ctx context.Context, opts options, out io.Writer) (resultErr error) {
	if opts.command == "create" {
		return createMigration(opts.path, opts.arg, out)
	}
	_, count, err := inspectMigrations(opts.path)
	if err != nil {
		return err
	}
	if err := config.LoadEnvFile(opts.envFile); err != nil {
		return errors.New("cannot load environment file")
	}
	cfg, err := config.LoadDatabase(ctx)
	if err != nil {
		return errors.New("invalid database configuration; check DB_* variables")
	}
	conn, err := pgx.ParseConfig(postgres.DSN(cfg))
	if err != nil {
		return errors.New("invalid database connection configuration")
	}
	conn.ConnectTimeout = cfg.ConnectTimeout
	conn.RuntimeParams["application_name"] = "posostavu-migrate"
	conn.RuntimeParams["statement_timeout"] = strconv.FormatInt(opts.timeout.Milliseconds(), 10)
	conn.RuntimeParams["lock_timeout"] = "5000"
	db := stdlib.OpenDB(*conn)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	err = db.PingContext(connectCtx)
	cancel()
	if err != nil {
		return errors.New("database unavailable; check connection settings and PostgreSQL")
	}
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{
		DatabaseName: cfg.Name, SchemaName: "public", StatementTimeout: opts.timeout,
	})
	if err != nil {
		return err
	}
	source, err := iofs.New(os.DirFS(opts.path), ".")
	if err != nil {
		return errors.Join(err, driver.Close())
	}
	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return errors.Join(err, source.Close(), driver.Close())
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		resultErr = errors.Join(resultErr, sourceErr, dbErr)
	}()
	m.LockTimeout = 5 * time.Second
	version, dirty, versionErr := m.Version()
	if versionErr != nil && !errors.Is(versionErr, migrate.ErrNilVersion) {
		return versionErr
	}
	if opts.command == "version" {
		if errors.Is(versionErr, migrate.ErrNilVersion) {
			_, err = fmt.Fprintln(out, "version: none; dirty: false")
		} else {
			_, err = fmt.Fprintf(out, "version: %d; dirty: %t\n", version, dirty)
		}
		return err
	}
	if dirty && opts.command != "force" {
		return migrate.ErrDirty{Version: int(version)}
	}
	if count == 0 && opts.command != "force" {
		if !errors.Is(versionErr, migrate.ErrNilVersion) {
			return errors.New("migration files are missing for an existing database version")
		}
		_, err = fmt.Fprintln(out, "No migrations to apply or revert.")
		return err
	}
	switch opts.command {
	case "up":
		err = m.Up()
	case "down":
		err = m.Steps(-1)
	case "down-all":
		err = m.Down()
	case "force":
		forced, parseErr := strconv.Atoi(opts.arg)
		if parseErr != nil {
			return errors.New("invalid forced version")
		}
		err = m.Force(forced)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		_, err = fmt.Fprintln(out, "No change.")
		return err
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Migration command completed.")
	return err
}

func safeError(err error) string {
	var dirty migrate.ErrDirty
	if errors.As(err, &dirty) {
		return fmt.Sprintf("Database migration version %d is dirty; inspect and repair the schema before an explicit force.", dirty.Version)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && sqlStatePattern.MatchString(pgErr.Code) {
		return "Migration failed (SQLSTATE " + pgErr.Code + "); inspect SQL and database version."
	}
	// Never print driver errors: they can include SQL, credentials or row data.
	return "Migration failed; check configuration, SQL file pairs, connection and database version."
}

var (
	namePattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)
	filePattern     = regexp.MustCompile(`^([0-9]{6})_([a-z][a-z0-9_]{0,79})\.(up|down)\.sql$`)
	sqlStatePattern = regexp.MustCompile(`^[A-Z0-9]{5}$`)
)

func inspectMigrations(path string) (maxVersion, count int, err error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, 0, err
	}
	versions := make(map[int]string)
	pairs := make(map[string]int)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		match := filePattern.FindStringSubmatch(entry.Name())
		if match == nil || entry.Type()&os.ModeSymlink != 0 {
			return 0, 0, errors.New("invalid migration file")
		}
		version, parseErr := strconv.Atoi(match[1])
		if parseErr != nil || version == 0 {
			return 0, 0, errors.New("invalid migration version")
		}
		if name, exists := versions[version]; exists && name != match[2] {
			return 0, 0, errors.New("duplicate migration version")
		}
		versions[version] = match[2]
		pairs[match[1]+"_"+match[2]]++
		maxVersion = max(maxVersion, version)
	}
	for _, files := range pairs {
		if files != 2 {
			return 0, 0, errors.New("migration requires both up and down files")
		}
	}
	return maxVersion, len(pairs), nil
}

func createMigration(path, name string, out io.Writer) (resultErr error) {
	if !namePattern.MatchString(name) {
		return errors.New("invalid migration name")
	}
	// Serialize sequence allocation, including creators using different names.
	// A stale lock after a killed process is removed manually, never by a rival.
	lockPath := filepath.Join(path, ".create.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.Remove(lockPath)) }()
	if err := lock.Close(); err != nil {
		return err
	}
	var created []string
	defer func() {
		if resultErr != nil {
			for _, path := range created {
				resultErr = errors.Join(resultErr, os.Remove(path))
			}
		}
	}()
	maxVersion, _, err := inspectMigrations(path)
	if err != nil {
		return err
	}
	if maxVersion >= 999999 {
		return errors.New("migration sequence exhausted")
	}
	base := fmt.Sprintf("%06d_%s", maxVersion+1, name)
	up := filepath.Join(path, base+".up.sql")
	down := filepath.Join(path, base+".down.sql")
	for _, filename := range []string{up, down} {
		file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		created = append(created, filename)
		_, writeErr := file.WriteString("BEGIN;\n\n-- Add the schema change and explicit application-role grants here.\n\nCOMMIT;\n")
		if err = errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "%s\n%s\n", up, down)
	return err
}
