package llm

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino-ext/components/model/openai"
)

// Role represents a task role with specific model requirements.
type Role string

const (
	RoleFast      Role = "fast"      // intent_classify, compliance_check
	RoleDefault   Role = "default"   // supervisor, ticket_handler
	RoleReasoning Role = "reasoning" // knowledge_rag answer synthesis
)

// Router selects a model by task role, falling back to RoleDefault.
type Router struct {
	models map[Role]model.BaseChatModel
}

// NewRouter creates a Router from environment variables.
//
// Env vars:
//   - LLM_CHAT_MODEL       (default: qwen2.5:7b) — used for RoleDefault and as fallback
//   - LLM_FAST_MODEL       (optional) — used for RoleFast, falls back to LLM_CHAT_MODEL
//   - LLM_REASONING_MODEL  (optional) — used for RoleReasoning, falls back to LLM_CHAT_MODEL
//   - OLLAMA_BASE_URL      (default: http://localhost:11434)
//   - LLM_API_KEY          (default: ollama)
func NewRouter(ctx context.Context) (*Router, error) {
	baseURL := envOrDefault("OLLAMA_BASE_URL", "http://localhost:11434") + "/v1"
	apiKey := envOrDefault("LLM_API_KEY", "ollama")
	defaultModel := envOrDefault("LLM_CHAT_MODEL", "qwen2.5:7b")

	models := make(map[Role]model.BaseChatModel, 3)

	defaultChat, err := newChatModel(ctx, baseURL, defaultModel, apiKey)
	if err != nil {
		return nil, fmt.Errorf("init default model: %w", err)
	}
	models[RoleDefault] = defaultChat

	fastModel := envOrDefault("LLM_FAST_MODEL", defaultModel)
	if fastModel != defaultModel {
		fastChat, err := newChatModel(ctx, baseURL, fastModel, apiKey)
		if err != nil {
			return nil, fmt.Errorf("init fast model: %w", err)
		}
		models[RoleFast] = fastChat
	} else {
		models[RoleFast] = defaultChat
	}

	reasoningModel := envOrDefault("LLM_REASONING_MODEL", defaultModel)
	if reasoningModel != defaultModel {
		reasoningChat, err := newChatModel(ctx, baseURL, reasoningModel, apiKey)
		if err != nil {
			return nil, fmt.Errorf("init reasoning model: %w", err)
		}
		models[RoleReasoning] = reasoningChat
	} else {
		models[RoleReasoning] = defaultChat
	}

	log.Printf("[router] default=%s fast=%s reasoning=%s base=%s",
		defaultModel, models[RoleFast], models[RoleReasoning], baseURL)

	return &Router{models: models}, nil
}

// Select returns the model for the given role, falling back to RoleDefault.
func (r *Router) Select(role Role) model.BaseChatModel {
	m, ok := r.models[role]
	if !ok {
		return r.models[RoleDefault]
	}
	return m
}

func newChatModel(ctx context.Context, baseURL, modelName, apiKey string) (model.BaseChatModel, error) {
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: baseURL,
		Model:   modelName,
		APIKey:  apiKey,
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
