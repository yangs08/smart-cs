package tracing

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
)

// Tracer implements callbacks.Handler for timing and error metrics.
type Tracer struct {
	startTimes map[string]time.Time
}

func NewTracer() *Tracer {
	return &Tracer{
		startTimes: make(map[string]time.Time),
	}
}

func (t *Tracer) Needed(_ context.Context, _ *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	switch timing {
	case callbacks.TimingOnStart, callbacks.TimingOnEnd, callbacks.TimingOnError:
		return true
	default:
		return false
	}
}

func (t *Tracer) OnStart(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
	t.startTimes[info.Name] = time.Now()
	slog.Debug("trace start", "name", info.Name, "type", info.Type)
	return ctx
}

func (t *Tracer) OnEnd(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
	if start, ok := t.startTimes[info.Name]; ok {
		slog.Debug("trace end", "name", info.Name, "duration", time.Since(start))
		delete(t.startTimes, info.Name)
	}
	return ctx
}

func (t *Tracer) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	slog.Error("trace error", "name", info.Name, "error", err)
	return ctx
}

func (t *Tracer) OnStartWithStreamInput(ctx context.Context, _ *callbacks.RunInfo, _ *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	return ctx
}

func (t *Tracer) OnEndWithStreamOutput(ctx context.Context, _ *callbacks.RunInfo, _ *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	return ctx
}
