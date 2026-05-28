package main

import (
	"context"
	"log"
	"os"

	"helpdesk-agent/agent"
	"helpdesk-agent/api"
	"helpdesk-agent/knowledge"
	"helpdesk-agent/llm"
	"helpdesk-agent/memory"
	"helpdesk-agent/tracing"

	"github.com/cloudwego/eino/adk"
	"github.com/gin-gonic/gin"
)

func main() {
	ctx := context.Background()

	// 1. Initialize model router from environment variables.
	router, err := llm.NewRouter(ctx)
	if err != nil {
		log.Fatalf("init model router: %v", err)
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
		log.Printf("[knowledge] qdrant unavailable, trying BM25 stats fallback: %v", err)
		if err := kb.LoadStats(statsPath); err != nil {
			log.Printf("[knowledge] no BM25 stats either, vector-only mode: %v", err)
		}
	}

	// 3. Build supervisor agent.
	supervisor, err := agent.BuildSupervisor(ctx, &agent.HelpDeskConfig{
		Router: router,
		KB:     kb,
	})
	if err != nil {
		log.Fatalf("build supervisor: %v", err)
	}

	// 4. Initialize memory layers.
	shortTerm := memory.NewShortTermMemory(20)

	dataDir := os.Getenv("MEMORY_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data/memory"
	}
	longTerm, err := memory.NewLongTermMemory(dataDir)
	if err != nil {
		log.Fatalf("init long-term memory: %v", err)
	}

	// 5. Create runner.
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           supervisor,
		EnableStreaming: true,
	})

	// 6. Start API server.
	tracer := tracing.NewTracer()
	_ = tracer

	server := api.NewServer(runner, shortTerm, longTerm)

	ginEngine := gin.Default()
	server.RegisterRoutes(ginEngine)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("helpdesk agent starting on :%s", port)
	if err := ginEngine.Run(":" + port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
