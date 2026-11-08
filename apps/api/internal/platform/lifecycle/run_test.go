package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestShutdownDrainsRequestBeforeCancellingItsContext(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	fixture := startServer(t, time.Second, func(c *fiber.Ctx) error {
		close(entered)
		select {
		case <-release:
			return c.SendStatus(http.StatusOK)
		case <-c.UserContext().Done():
			return c.SendStatus(http.StatusServiceUnavailable)
		}
	})
	response := callServer(fixture.address)
	await(t, entered)
	fixture.stop()
	if fixture.requestContext.Err() != nil {
		t.Fatal("ordinary shutdown cancelled the active request before draining")
	}
	close(release)
	result := awaitResponse(t, response)
	if result.err != nil || result.status != http.StatusOK {
		t.Fatalf("request was not drained: status=%d, error=%v", result.status, result.err)
	}
	await(t, fixture.finished)
	if fixture.err != nil {
		t.Fatal(fixture.err)
	}
}

func TestShutdownDeadlineCancelsOutstandingOperation(t *testing.T) {
	t.Parallel()
	entered, exited := make(chan struct{}), make(chan struct{})
	fixture := startServer(t, 20*time.Millisecond, func(c *fiber.Ctx) error {
		close(entered)
		<-c.UserContext().Done()
		close(exited)
		return c.SendStatus(http.StatusServiceUnavailable)
	})
	response := callServer(fixture.address)
	await(t, entered)
	fixture.stop()
	await(t, fixture.finished)
	await(t, exited)
	_ = awaitResponse(t, response)
	if !errors.Is(fixture.err, ErrShutdown) {
		t.Fatalf("shutdown deadline was not reported: %v", fixture.err)
	}
	if !errors.Is(fixture.requestContext.Err(), context.Canceled) {
		t.Fatal("outstanding operation was not cancelled")
	}
}

func TestCancellationDuringListenDoesNotLeaveServerRunning(t *testing.T) {
	t.Parallel()
	// The hook runs before fasthttp registers its listener. This reproduces
	// the startup/shutdown ordering that a pre-bound socket alone cannot fix.
	for range 10 {
		ctx, stop := context.WithCancel(context.Background())
		requestContext, stopRequests := context.WithCancel(context.Background())
		app := newApp()
		var address string
		app.Hooks().OnListen(func(data fiber.ListenData) error {
			address = net.JoinHostPort(data.Host, data.Port)
			stop()
			return nil
		})
		if err := Run(ctx, app, "127.0.0.1:0", time.Second, stopRequests, testLogger()); err != nil {
			t.Fatal(err)
		}
		if requestContext.Err() == nil {
			t.Fatal("request context remained active after shutdown")
		}
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			_ = connection.Close()
			t.Fatal("HTTP listener remained open after cancellation")
		}
	}
}

func TestOccupiedPortFailsAndCancelsRequestContext(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	requestContext, stopRequests := context.WithCancel(context.Background())
	err = Run(context.Background(), newApp(), listener.Addr().String(), time.Second, stopRequests, testLogger())
	if !errors.Is(err, ErrListen) || requestContext.Err() == nil {
		t.Fatal("listener failure did not abort the service")
	}
}

type serverFixture struct {
	address        string
	requestContext context.Context
	stop           context.CancelFunc
	finished       chan struct{}
	err            error
}

func startServer(t *testing.T, shutdownTimeout time.Duration, handler fiber.Handler) *serverFixture {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	requestContext, stopRequests := context.WithCancel(context.Background())
	fixture := &serverFixture{requestContext: requestContext, stop: stop, finished: make(chan struct{})}
	app := newApp()
	app.Use(func(c *fiber.Ctx) error { c.SetUserContext(requestContext); return c.Next() })
	app.Get("/", handler)
	listening := make(chan string, 1)
	app.Hooks().OnListen(func(data fiber.ListenData) error {
		listening <- net.JoinHostPort(data.Host, data.Port)
		return nil
	})
	go func() {
		fixture.err = Run(ctx, app, "127.0.0.1:0", shutdownTimeout, stopRequests, testLogger())
		close(fixture.finished)
	}()
	t.Cleanup(func() { stop(); await(t, fixture.finished) })
	select {
	case fixture.address = <-listening:
	case <-fixture.finished:
		t.Fatalf("HTTP server failed to start: %v", fixture.err)
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP server did not start")
	}
	return fixture
}

func newApp() *fiber.App {
	return fiber.New(fiber.Config{
		DisableStartupMessage: true, DisableKeepalive: true,
		ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second,
	})
}

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

type responseResult struct {
	status int
	err    error
}

func callServer(address string) <-chan responseResult {
	result := make(chan responseResult, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://" + address + "/")
		if err != nil {
			result <- responseResult{err: err}
			return
		}
		_, err = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		result <- responseResult{status: response.StatusCode, err: err}
	}()
	return result
}

func awaitResponse(t *testing.T, response <-chan responseResult) responseResult {
	t.Helper()
	select {
	case result := <-response:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not finish")
		return responseResult{}
	}
}

func await(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not finish")
	}
}
