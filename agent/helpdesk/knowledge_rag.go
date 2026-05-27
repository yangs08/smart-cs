package helpdesk

import (
	"context"
	"fmt"
	"strings"

	"helpdesk-agent/knowledge"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type RAGInput struct {
	Query string `json:"query" jsonschema:"-"`
}

// RAGSearchTool creates a tool that performs hybrid search and synthesizes an answer.
func RAGSearchTool(llm model.BaseChatModel, store *knowledge.HybridStore) (tool.InvokableTool, error) {
	return utils.InferTool[*RAGInput, string](
		"knowledge_search",
		"Search the knowledge base for information related to the query. "+
			"Returns a synthesized answer with source references.",
		func(ctx context.Context, input *RAGInput) (string, error) {
			if input.Query == "" {
				return "", fmt.Errorf("empty query")
			}

			// 1. Hybrid search: BM25 + vector + RRF
			results, hasResult, err := store.Search(ctx, input.Query, 3)
			if err != nil {
				return fmt.Sprintf(`{"answer":"Search failed: %v","sources":[],"relevant":false}`, err), nil
			}

			// 2. Check confidence threshold
			if !hasResult || len(results) == 0 {
				return `{"answer":"I couldn't find relevant information in the knowledge base.","sources":[],"relevant":false}`, nil
			}

			// 3. Build context from top results
			var contextBuilder strings.Builder
			var sources []string
			for _, sd := range results {
				contextBuilder.WriteString(fmt.Sprintf("Title: %s\nContent: %s\n\n", sd.Doc.Title, sd.Doc.Content))
				sources = append(sources, sd.Doc.ID)
			}

			// 4. Synthesize answer using LLM
			answer, err := synthesizeAnswer(ctx, llm, input.Query, contextBuilder.String())
			if err != nil {
				answer = fmt.Sprintf("Found %d relevant documents:\n%s", len(results), contextBuilder.String())
			}

			// 5. Return JSON with confidence info
			srcJSON := `"` + strings.Join(sources, `","`) + `"`
			topScore := results[0].Score
			return fmt.Sprintf(`{"answer":"%s","sources":[%s],"relevant":true,"confidence":%.4f}`,
				escapeJSON(answer), srcJSON, topScore), nil
		},
	)
}

func synthesizeAnswer(ctx context.Context, llm model.BaseChatModel, query, contextStr string) (string, error) {
	sysPrompt := schema.SystemMessage(fmt.Sprintf(`You are a helpful customer service assistant.
Use the following knowledge base context to answer the user's question.
If the context doesn't contain enough information, say so clearly.
Keep your answer concise and friendly.

Context:
%s`, contextStr))

	prompt := schema.UserMessage(query)

	resp, err := llm.Generate(ctx, []*schema.Message{sysPrompt, prompt})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
