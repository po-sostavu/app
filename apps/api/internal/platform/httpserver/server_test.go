package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/po-sostavu/app/apps/api/internal/platform/config"
)

type pingFunc func(context.Context) error

func (fn pingFunc) PingContext(ctx context.Context) error { return fn(ctx) }

func testHTTPConfig() config.HTTP {
	return config.HTTP{
		RequestTimeout: time.Second, ReadTimeout: time.Second,
		WriteTimeout: 2 * time.Second, IdleTimeout: time.Second,
		BodyLimit: 1024, Concurrency: 16,
	}
}

func TestReadinessFailureIsBoundedAndDoesNotDisableLiveness(t *testing.T) {
	t.Parallel()
	db := pingFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	app := New(context.Background(), testHTTPConfig(), db, 10*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	ready := request(t, app, "/readyz")
	defer func() { _ = ready.Body.Close() }()
	if ready.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readiness status: %d", ready.StatusCode)
	}
	var failure errorResponse
	if err := json.NewDecoder(ready.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "dependency_unavailable" {
		t.Fatalf("unexpected readiness error code: %s", failure.Error.Code)
	}
	live := request(t, app, "/livez")
	defer func() { _ = live.Body.Close() }()
	if live.StatusCode != http.StatusOK {
		t.Fatalf("liveness depends on the database: %d", live.StatusCode)
	}
}

func TestErrorsAndLogsDoNotExposeDependencyOrRequestData(t *testing.T) {
	t.Parallel()
	const secret = "private-value-must-not-appear"
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	app := New(context.Background(), testHTTPConfig(), pingFunc(func(context.Context) error {
		return errors.New("postgres://user:" + secret + "@host/db")
	}), time.Second, logger)
	app.Get("/panic", func(*fiber.Ctx) error { panic(secret) })
	for _, test := range []struct {
		path   string
		status int
	}{
		{"/readyz", http.StatusServiceUnavailable},
		{"/missing?password=" + secret, http.StatusNotFound},
		{"/panic", http.StatusInternalServerError},
	} {
		req := httptest.NewRequest(http.MethodGet, test.path, nil)
		req.Header.Set(fiber.HeaderXRequestID, secret)
		response, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != test.status {
			t.Fatalf("unexpected status for test route: %d", response.StatusCode)
		}
		if strings.Contains(string(body), secret) || strings.Contains(logs.String(), secret) {
			t.Fatal("response or log exposes supplied data")
		}
		var failure errorResponse
		if err := json.Unmarshal(body, &failure); err != nil {
			t.Fatal(err)
		}
		if failure.Error.RequestID == "" || response.Header.Get(fiber.HeaderXRequestID) != failure.Error.RequestID {
			t.Fatal("response does not carry the generated request ID")
		}
	}
}

func TestRequestDeadlineReachesHandlerAndReturnsSafeTimeout(t *testing.T) {
	t.Parallel()
	cfg := testHTTPConfig()
	cfg.RequestTimeout = 10 * time.Millisecond
	app := New(context.Background(), cfg, pingFunc(func(context.Context) error { return nil }), time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	app.Get("/slow", func(c *fiber.Ctx) error {
		<-c.UserContext().Done()
		return c.UserContext().Err()
	})
	response := request(t, app, "/slow")
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("deadline status: %d", response.StatusCode)
	}
}

func request(t *testing.T, app *fiber.App, path string) *http.Response {
	t.Helper()
	response, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatal(err)
	}
	return response
}
