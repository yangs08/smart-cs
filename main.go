package main

import (
	"context"
	"log"
	"os"

	"helpdesk-agent/agent"
	"helpdesk-agent/api"
	"helpdesk-agent/llm"
	"helpdesk-agent/memory"
	"helpdesk-agent/tracing"

	"github.com/cloudwego/eino/adk"
	"github.com/gin-gonic/gin"
)

func main() {
	ctx := context.Background()

	// 1. Initialize LLM registry from environment variables.
	reg, err := llm.NewRegistry(ctx)
	if err != nil {
		log.Fatalf("init llm registry: %v", err)
	}

	// 2. Build supervisor agent.
	supervisor, err := agent.BuildSupervisor(ctx, &agent.HelpDeskConfig{
		DeepSeek: reg.Get("deepseek"),
		Gemini:   reg.Get("gemini"),
	})
	if err != nil {
		log.Fatalf("build supervisor: %v", err)
	}

	// 3. Initialize memory layers.
	shortTerm := memory.NewShortTermMemory(20)

	dataDir := os.Getenv("MEMORY_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data/memory"
	}
	longTerm, err := memory.NewLongTermMemory(dataDir)
	if err != nil {
		log.Fatalf("init long-term memory: %v", err)
	}
	_ = longTerm

	// 4. Create runner.
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           supervisor,
		EnableStreaming: true,
		CheckPointStore: nil, // Enable for multi-turn persistence
	})

	// 5. Start API server.
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
