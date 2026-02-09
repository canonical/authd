package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

// SimpleHandler writes logs in the format: <timestamp> <level> <message>.
type SimpleHandler struct {
	slog.TextHandler
	w io.Writer
}

// NewSimpleHandler creates a new SimpleHandler that writes to the provided io.Writer.
func NewSimpleHandler(w io.Writer, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{
		Level: level,
	}
	return &SimpleHandler{
		TextHandler: *slog.NewTextHandler(w, opts),
		w:           w,
	}
}

// Handle implements the slog.Handler interface.
func (h *SimpleHandler) Handle(ctx context.Context, r slog.Record) error {
	t := r.Time.Format("15:04:05")
	_, err := fmt.Fprintf(h.w, "%s %s %s\n", t, r.Level.String(), r.Message)
	return err
}

// Enabled checks if the handler is enabled for the given log level.
func (h *SimpleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.TextHandler.Enabled(ctx, level)
}

// WithAttrs returns a new SimpleHandler with the specified attributes.
func (h *SimpleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	textHandler, ok := h.TextHandler.WithAttrs(attrs).(*slog.TextHandler)
	if !ok {
		panic("WithAttrs did not return a *slog.TextHandler")
	}

	return &SimpleHandler{
		TextHandler: *textHandler,
		w:           h.w,
	}
}

// WithGroup returns a new SimpleHandler with the specified group name.
func (h *SimpleHandler) WithGroup(name string) slog.Handler {
	textHandler, ok := h.TextHandler.WithGroup(name).(*slog.TextHandler)
	if !ok {
		panic("WithGroup did not return a *slog.TextHandler")
	}

	return &SimpleHandler{
		TextHandler: *textHandler,
		w:           h.w,
	}
}
