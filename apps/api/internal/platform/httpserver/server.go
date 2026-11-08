package httpserver

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/po-sostavu/app/apps/api/internal/platform/config"
)

type Pinger interface {
	PingContext(context.Context) error
}

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type statusResponse struct {
	Status string `json:"status"`
}

type requestIDKey struct{}
type errorCodeKey struct{}

var errNotReady = errors.New("service is not ready")

func New(requestRoot context.Context, cfg config.HTTP, db Pinger, queryTimeout time.Duration, logger *slog.Logger) *fiber.App {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		ReadTimeout:           cfg.ReadTimeout,
		WriteTimeout:          cfg.WriteTimeout,
		IdleTimeout:           cfg.IdleTimeout,
		BodyLimit:             cfg.BodyLimit,
		Concurrency:           cfg.Concurrency,
		ErrorHandler:          handleError,
	})
	app.Use(func(c *fiber.Ctx) error {
		id := rand.Text()
		c.Locals(requestIDKey{}, id)
		c.Set(fiber.HeaderXRequestID, id)
		c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
		c.Set(fiber.HeaderCacheControl, "no-store")
		ctx, cancel := context.WithTimeout(requestRoot, cfg.RequestTimeout)
		defer cancel()
		// fasthttp's RequestCtx does not cancel on client disconnect. Every SQL
		// or external call must use this explicit context and respect its deadline.
		c.SetUserContext(ctx)
		started := time.Now()
		err := c.Next()
		if err != nil {
			err = handleError(c, err)
		}
		fields := []any{
			"request_id", id,
			"method", c.Method(),
			"route", c.Route().Path,
			"status", c.Response().StatusCode(),
			"duration_ms", time.Since(started).Milliseconds(),
		}
		if code, ok := c.Locals(errorCodeKey{}).(string); ok {
			fields = append(fields, "code", code)
		}
		logger.InfoContext(ctx, "HTTP request", fields...)
		return err
	})
	app.Use(recover.New())
	app.Get("/livez", func(c *fiber.Ctx) error {
		return c.JSON(statusResponse{Status: "ok"})
	})
	app.Get("/readyz", func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), queryTimeout)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			logger.WarnContext(ctx, "Readiness check failed",
				"request_id", requestID(c), "code", "dependency_unavailable", "dependency", "postgresql")
			return errNotReady
		}
		return c.JSON(statusResponse{Status: "ok"})
	})
	return app
}

func handleError(c *fiber.Ctx, err error) error {
	status := fiber.StatusInternalServerError
	code, message := "internal_error", "Internal server error"
	switch {
	case errors.Is(err, errNotReady):
		status, code, message = fiber.StatusServiceUnavailable, "dependency_unavailable", "Service is not ready"
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = fiber.StatusGatewayTimeout, "request_timeout", "Request timed out"
	case errors.Is(err, context.Canceled):
		status, code, message = fiber.StatusServiceUnavailable, "service_unavailable", "Service is unavailable"
	default:
		var fiberError *fiber.Error
		if errors.As(err, &fiberError) {
			switch fiberError.Code {
			case fiber.StatusBadRequest:
				status, code, message = fiber.StatusBadRequest, "bad_request", "Invalid request"
			case fiber.StatusNotFound:
				status, code, message = fiber.StatusNotFound, "not_found", "Resource not found"
			case fiber.StatusMethodNotAllowed:
				status, code, message = fiber.StatusMethodNotAllowed, "method_not_allowed", "Method is not allowed"
			case fiber.StatusRequestTimeout:
				status, code, message = fiber.StatusRequestTimeout, "request_timeout", "Request timed out"
			case fiber.StatusRequestEntityTooLarge:
				status, code, message = fiber.StatusRequestEntityTooLarge, "payload_too_large", "Request body is too large"
			case fiber.StatusRequestHeaderFieldsTooLarge:
				status, code, message = fiber.StatusRequestHeaderFieldsTooLarge, "headers_too_large", "Request headers are too large"
			}
		}
	}
	id := requestID(c)
	c.Locals(errorCodeKey{}, code)
	c.Set(fiber.HeaderXRequestID, id)
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	return c.Status(status).JSON(errorResponse{Error: errorDetail{Code: code, Message: message, RequestID: id}})
}

func requestID(c *fiber.Ctx) string {
	if id, ok := c.Locals(requestIDKey{}).(string); ok {
		return id
	}
	// Transport errors can occur before application middleware runs.
	id := rand.Text()
	c.Locals(requestIDKey{}, id)
	return id
}
