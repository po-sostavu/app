package postgres

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	"github.com/po-sostavu/app/apps/api/internal/platform/config"
)

// DSN builds an escaped connection string. It contains a password and must
// never be logged or passed in process arguments.
func DSN(cfg config.Database) string {
	connectSeconds := int64(cfg.ConnectTimeout / time.Second)
	if cfg.ConnectTimeout%time.Second != 0 {
		connectSeconds++
	}
	params := url.Values{
		"sslmode":          {cfg.SSLMode},
		"connect_timeout":  {strconv.FormatInt(connectSeconds, 10)},
		"application_name": {"posostavu-api"},
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path:     "/" + cfg.Name,
		RawQuery: params.Encode(),
	}
	return u.String()
}

func Open(ctx context.Context, cfg config.Database) (*sqlx.DB, error) {
	connection, err := pgx.ParseConfig(DSN(cfg))
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	pool := stdlib.OpenDB(*connection)
	db := sqlx.NewDb(pool, "pgx")
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(connectCtx); err != nil {
		_ = db.Close()
		return nil, errors.New("PostgreSQL startup check failed")
	}
	return db, nil
}
