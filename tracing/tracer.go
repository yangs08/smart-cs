package tracing

import (
	"context"
	"log"
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
	log.Printf("[TRACE START] %s | type=%s", info.Name, info.Type)
	return ctx
}

func (t *Tracer) OnEnd(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
	if start, ok := t.startTimes[info.Name]; ok {
		log.Printf("[TRACE END]   %s | duration=%v", info.Name, time.Since(start))
		delete(t.startTimes, info.Name)
	}
	return ctx
}

func (t *Tracer) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	log.Printf("[TRACE ERROR] %s | err=%v", info.Name, err)
	return ctx
}

func (t *Tracer) OnStartWithStreamInput(ctx context.Context, _ *callbacks.RunInfo, _ *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	return ctx
}

func (t *Tracer) OnEndWithStreamOutput(ctx context.Context, _ *callbacks.RunInfo, _ *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	return ctx
}
