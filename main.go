package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"helpdesk-agent/agent"
	"helpdesk-agent/api"
	"helpdesk-agent/knowledge"
	"helpdesk-agent/llm"
	"helpdesk-agent/memory"
	"helpdesk-agent/tracing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/gin-gonic/gin"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Initialize model router from environment variables.
	router, err := llm.NewRouter(ctx)
	if err != nil {
		slog.Error("init model router", "error", err)
		os.Exit(1)
	}

	// 2. Initialize hybrid knowledge store.
	embedder := knowledge.NewOllamaEmbedder(
		envOrDefault("OLLAMA_BASE_URL", "http://localhost:11434"),
		envOrDefault("LLM_EMBED_MODEL", "nomic-embed-text"),
	)
	kb, _ := knowledge.NewHybridStore(
		envOrDefault("QDRANT_ADDR", "localhost:6333"),
		envOrDefault("QDRANT_COLLECTION", "helpdesk_kb"),
		embedder,
	)

	// 3. Init knowledge base: load docs → embed → upsert Qdrant → persist BM25 stats.
	statsPath := "./data/bm25_stats.json"
	kb.SetStatsPath(statsPath)
	loader := knowledge.NewStaticDocLoader(knowledge.DefaultDocs())
	if err := knowledge.InitKnowledgeBase(ctx, kb, loader); err != nil {
		slog.Warn("qdrant unavailable, trying BM25 stats fallback", "error", err)
		if err := kb.LoadStats(statsPath); err != nil {
			slog.Warn("no BM25 stats either, vector-only mode", "error", err)
		}
	}

	// 3. Build supervisor agent.
	supervisor, err := agent.BuildSupervisor(ctx, &agent.HelpDeskConfig{
		Router: router,
		KB:     kb,
	})
	if err != nil {
		slog.Error("build supervisor", "error", err)
		os.Exit(1)
	}

	// 4. Initialize memory layers.
	shortTerm := memory.NewShortTermMemory(20)

	dataDir := os.Getenv("MEMORY_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data/memory"
	}
	longTerm, err := memory.NewLongTermMemory(dataDir)
	if err != nil {
		slog.Error("init long-term memory", "error", err)
		os.Exit(1)
	}

	// 5. Create runner.
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           supervisor,
		EnableStreaming: true,
	})

	// 6. Start API server.
	tracer := tracing.NewTracer()
	callbacks.InitCallbackHandlers([]callbacks.Handler{tracer})

	server := api.NewServer(runner, shortTerm, longTerm, kb)

	ginEngine := gin.Default()
	server.RegisterRoutes(ginEngine)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	slog.Info("helpdesk agent starting", "port", port)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: ginEngine,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("forced shutdown", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
