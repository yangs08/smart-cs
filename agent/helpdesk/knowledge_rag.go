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
	Query string `json:"query" description:"The search query for knowledge base retrieval"`
}

// RAGSearchTool creates a tool that performs hybrid search and synthesizes an answer.
func RAGSearchTool(llm model.BaseChatModel, store *knowledge.HybridStore) (tool.InvokableTool, error) {
	return utils.InferTool[*RAGInput, SearchResult](
		"knowledge_search",
		"Search the knowledge base for information related to the query. "+
			"Returns a synthesized answer with source references.",
		func(ctx context.Context, input *RAGInput) (SearchResult, error) {
			if input.Query == "" {
				return SearchResult{}, fmt.Errorf("empty query")
			}

			// 1. Hybrid search: BM25 + vector + RRF
			results, hasResult, err := store.Search(ctx, input.Query, 3)
			if err != nil {
				return SearchResult{Answer: fmt.Sprintf("Search failed: %v", err)}, nil
			}

			// 2. Check confidence threshold
			if !hasResult || len(results) == 0 {
				return SearchResult{
					Answer:   "I couldn't find relevant information in the knowledge base.",
					Relevant: false,
				}, nil
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

			// 5. Return structured result (Eino auto-serializes to JSON)
			return SearchResult{
				Answer:     answer,
				Sources:    sources,
				Relevant:   true,
				Confidence: results[0].Score,
			}, nil
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

