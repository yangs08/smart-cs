package llm

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

// well-known model names
const (
	ModelDeepSeek = "deepseek"
	ModelGemini   = "gemini"
)

type Registry struct {
	models map[string]*openai.ChatModel
}

func NewRegistry(ctx context.Context) (*Registry, error) {
	models := make(map[string]*openai.ChatModel)

	for _, name := range []string{ModelDeepSeek, ModelGemini} {
		prefix := fmt.Sprintf("LLM_%s", name)
		baseURL := envOrDefault(prefix+"_BASE_URL", "https://api.openai.com/v1")
		model := envOrDefault(prefix+"_MODEL", name)
		apiKey := os.Getenv(prefix + "_API_KEY")

		if apiKey == "" {
			log.Printf("[llm] %s: no API key (set %s_API_KEY), skipping", name, prefix)
			continue
		}

		m, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
			BaseURL: baseURL,
			Model:   model,
			APIKey:  apiKey,
		})
		if err != nil {
			return nil, fmt.Errorf("init %s: %w", name, err)
		}
		models[name] = m
		log.Printf("[llm] %s -> %s (%s)", name, baseURL, model)
	}

	return &Registry{models: models}, nil
}

func (r *Registry) Get(name string) *openai.ChatModel {
	return r.models[name]
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
