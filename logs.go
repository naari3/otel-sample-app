package main

import (
	"context"
	"log/slog"
	"strconv"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type LogHandler struct {
	Next slog.Handler
}

type HandlerFn func(slog.Handler) slog.Handler

func NewLogHandler(s slog.Handler) *LogHandler {
	return &LogHandler{
		Next: s,
	}
}

func convertTraceID(id string) string {
	if len(id) < 16 {
		return ""
	}
	if len(id) > 16 {
		id = id[16:]
	}
	intValue, err := strconv.ParseUint(id, 16, 64)
	if err != nil {
		return ""
	}
	return strconv.FormatUint(intValue, 10)
}

func (h *LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx == nil {
		return h.Next.Handle(ctx, r)
	}

	span := trace.SpanFromContext(ctx)
	if span == nil || !span.IsRecording() {
		return h.Next.Handle(ctx, r)
	}

	spanContext := span.SpanContext()
	if spanContext.HasTraceID() {
		traceID := spanContext.TraceID().String()
		r.AddAttrs(slog.String("trace_id", traceID))
		r.AddAttrs(slog.String("dd.trace_id", convertTraceID(traceID)))
	}

	if spanContext.HasSpanID() {
		spanID := spanContext.SpanID().String()
		r.AddAttrs(slog.String("span_id", spanID))
		r.AddAttrs(slog.String("dd.span_id", convertTraceID(spanID)))
	}

	if r.Level >= slog.LevelError {
		span.SetStatus(codes.Error, r.Message)
	}

	return h.Next.Handle(ctx, r)
}

func (h LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{
		Next: h.Next.WithAttrs(attrs),
	}
}

func (h LogHandler) WithGroup(name string) slog.Handler {
	return &LogHandler{
		Next: h.Next.WithGroup(name),
	}
}

func (h LogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.Next.Enabled(ctx, level)
}
