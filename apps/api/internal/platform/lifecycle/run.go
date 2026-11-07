package lifecycle

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

var (
	ErrListen   = errors.New("HTTP listener failed")
	ErrShutdown = errors.New("HTTP shutdown deadline exceeded")
)

// Run owns the listener. stopRequests is separate from ctx so an ordinary
// shutdown drains current requests before cancelling outstanding operations.
func Run(ctx context.Context, app *fiber.App, addr string, shutdownTimeout time.Duration, stopRequests context.CancelFunc, logger *slog.Logger) error {
	defer stopRequests()
	if ctx.Err() != nil {
		return nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return ErrListen
	}
	defer func() { _ = listener.Close() }()
	started := &startedListener{Listener: listener, ready: make(chan struct{})}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- app.Listener(started) }()
	// Fiber registers its listener before the first Accept. Waiting for that
	// point also handles a shutdown signal arriving during server startup.
	select {
	case <-started.ready:
		logger.Info("HTTP server started", "address", listener.Addr().String())
	case err := <-serveErrors:
		if err == nil {
			logger.Error("HTTP server stopped before accepting connections", "code", "listener_stopped")
		}
		return ErrListen
	}
	select {
	case err := <-serveErrors:
		if err == nil {
			logger.Error("HTTP server stopped unexpectedly", "code", "listener_stopped")
		}
		return ErrListen
	case <-ctx.Done():
	}
	logger.Info("HTTP server stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		stopRequests()
		return ErrShutdown
	}
	stopRequests()
	// Shutdown has closed the listener. Join its goroutine before returning.
	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return ErrListen
		}
	case <-shutdownCtx.Done():
		return ErrShutdown
	}
	logger.Info("HTTP server stopped")
	return nil
}

type startedListener struct {
	net.Listener
	ready chan struct{}
	once  sync.Once
}

func (listener *startedListener) Accept() (net.Conn, error) {
	listener.once.Do(func() { close(listener.ready) })
	return listener.Listener.Accept()
}
