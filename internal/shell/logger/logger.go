package logger

import (
	"context"
	"io"
	"log/slog"

	"agent-dashboard/internal/core/scrub"
)

// ScrubbingHandler wraps a slog.Handler and scrubs all string values and messages.
type ScrubbingHandler struct {
	inner         slog.Handler
	customSecrets []string
}

// NewScrubbingHandler creates a new handler that scrubs secrets from log outputs.
func NewScrubbingHandler(inner slog.Handler, customSecrets ...string) *ScrubbingHandler {
	return &ScrubbingHandler{
		inner:         inner,
		customSecrets: customSecrets,
	}
}

func (h *ScrubbingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *ScrubbingHandler) Handle(ctx context.Context, r slog.Record) error {
	scrubbedMsg := scrub.Scrub(r.Message, h.customSecrets...)
	newRecord := slog.NewRecord(r.Time, r.Level, scrubbedMsg, r.PC)

	r.Attrs(func(a slog.Attr) bool {
		newRecord.AddAttrs(h.scrubAttr(a))
		return true
	})

	return h.inner.Handle(ctx, newRecord)
}

func (h *ScrubbingHandler) scrubAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, scrub.Scrub(a.Value.String(), h.customSecrets...))
	}
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		scrubbed := make([]slog.Attr, len(attrs))
		for i, sub := range attrs {
			scrubbed[i] = h.scrubAttr(sub)
		}
		return slog.Attr{
			Key:   a.Key,
			Value: slog.GroupValue(scrubbed...),
		}
	}
	return a
}

func (h *ScrubbingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	scrubbed := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		scrubbed[i] = h.scrubAttr(a)
	}
	return &ScrubbingHandler{
		inner:         h.inner.WithAttrs(scrubbed),
		customSecrets: h.customSecrets,
	}
}

func (h *ScrubbingHandler) WithGroup(name string) slog.Handler {
	return &ScrubbingHandler{
		inner:         h.inner.WithGroup(name),
		customSecrets: h.customSecrets,
	}
}

// New creates a default JSON structured logger that scrubs sensitive data.
func New(w io.Writer, level slog.Level, customSecrets ...string) *slog.Logger {
	baseHandler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(NewScrubbingHandler(baseHandler, customSecrets...))
}
