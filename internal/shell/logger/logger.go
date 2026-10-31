package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"agent-dashboard/internal/core/scrub"
)

// ScrubbingHandler wraps a slog.Handler and scrubs all string values and messages.
type ScrubbingHandler struct {
	inner    slog.Handler
	ops      []handlerOp
	registry *secretRegistry
}

type handlerOp struct {
	attrs []slog.Attr
	group string
}

type secretRegistry struct {
	mu     sync.RWMutex
	values []string
}

func RegisterSecrets(l *slog.Logger, secrets ...string) {
	if h, ok := l.Handler().(*ScrubbingHandler); ok {
		h.registry.mu.Lock()
		defer h.registry.mu.Unlock()
		for _, s := range secrets {
			if s != "" {
				exists := false
				for _, old := range h.registry.values {
					if old == s {
						exists = true
						break
					}
				}
				if !exists {
					h.registry.values = append(h.registry.values, s)
				}
			}
		}
	}
}

func Scrub(l *slog.Logger, value string) string {
	if h, ok := l.Handler().(*ScrubbingHandler); ok {
		return h.scrub(value)
	}
	return scrub.Scrub(value)
}

func (h *ScrubbingHandler) scrub(value string) string {
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()
	return scrub.Scrub(value, h.registry.values...)
}

// NewScrubbingHandler creates a new handler that scrubs secrets from log outputs.
func NewScrubbingHandler(inner slog.Handler, customSecrets ...string) *ScrubbingHandler {
	return &ScrubbingHandler{
		inner:    inner,
		registry: &secretRegistry{values: append([]string(nil), customSecrets...)},
	}
}

func (h *ScrubbingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *ScrubbingHandler) Handle(ctx context.Context, r slog.Record) error {
	scrubbedMsg := h.scrub(r.Message)
	newRecord := slog.NewRecord(r.Time, r.Level, scrubbedMsg, r.PC)

	r.Attrs(func(a slog.Attr) bool {
		newRecord.AddAttrs(h.scrubAttr(a))
		return true
	})

	inner := h.inner
	for _, op := range h.ops {
		if op.group != "" {
			inner = inner.WithGroup(h.scrub(op.group))
		} else {
			attrs := make([]slog.Attr, len(op.attrs))
			for i, a := range op.attrs {
				attrs[i] = h.scrubAttr(a)
			}
			inner = inner.WithAttrs(attrs)
		}
	}
	return inner.Handle(ctx, newRecord)
}

func (h *ScrubbingHandler) scrubAttr(a slog.Attr) slog.Attr {
	a.Key = h.scrub(a.Key)
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, h.scrub(a.Value.String()))
	}
	if a.Value.Kind() == slog.KindAny {
		// Serialize before scrubbing so nested structs, maps, errors and Stringers
		// cannot bypass the same redaction applied to ordinary string attributes.
		v := a.Value.Any()
		if err, ok := v.(error); ok {
			return slog.String(a.Key, h.scrub(err.Error()))
		}
		if str, ok := v.(fmt.Stringer); ok {
			return slog.String(a.Key, h.scrub(str.String()))
		}
		data, err := json.Marshal(v)
		if err != nil {
			return slog.String(a.Key, "[unserializable value]")
		}
		var safe any
		if json.Unmarshal(data, &safe) != nil {
			return slog.String(a.Key, "[redacted value]")
		}
		return slog.Any(a.Key, h.scrubJSON(safe))
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
	ops := append(append([]handlerOp(nil), h.ops...), handlerOp{attrs: append([]slog.Attr(nil), attrs...)})
	return &ScrubbingHandler{inner: h.inner, registry: h.registry, ops: ops}
}
func (h *ScrubbingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	ops := append(append([]handlerOp(nil), h.ops...), handlerOp{group: name})
	return &ScrubbingHandler{inner: h.inner, registry: h.registry, ops: ops}
}

// New creates a default JSON structured logger that scrubs sensitive data.
func New(w io.Writer, level slog.Level, customSecrets ...string) *slog.Logger {
	baseHandler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(NewScrubbingHandler(baseHandler, customSecrets...))
}

func (h *ScrubbingHandler) scrubJSON(value any) any {
	switch v := value.(type) {
	case string:
		return h.scrub(v)
	case []any:
		for i, x := range v {
			v[i] = h.scrubJSON(x)
		}
		return v
	case map[string]any:
		result := make(map[string]any, len(v))
		for k, x := range v {
			result[h.scrub(k)] = h.scrubJSON(x)
		}
		return result
	default:
		return v
	}
}
