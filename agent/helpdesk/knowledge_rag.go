package helpdesk

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// KnowledgeDoc is an entry in the local knowledge base.
type KnowledgeDoc struct {
	ID      string
	Title   string
	Content string
	Tags    []string
}

// knowledgeBase is a thread-safe in-memory document store.
type knowledgeBase struct {
	mu   sync.RWMutex
	docs []KnowledgeDoc
}

var globalKB = &knowledgeBase{
	docs: []KnowledgeDoc{
		{ID: "ship_01", Title: "Shipping Policy", Content: "Standard shipping takes 3-5 business days. Express shipping takes 1-2 business days. Free shipping on orders over $50.", Tags: []string{"shipping", "delivery", "policy"}},
		{ID: "ret_01", Title: "Return Policy", Content: "Items can be returned within 30 days of delivery. Items must be unused and in original packaging. Refund will be processed within 5-7 business days after we receive the return.", Tags: []string{"return", "refund", "policy"}},
		{ID: "acc_01", Title: "Account Management", Content: "You can reset your password from the login page. To update your profile, go to Settings > Profile. For security, enable two-factor authentication.", Tags: []string{"account", "password", "security"}},
		{ID: "pay_01", Title: "Payment Methods", Content: "We accept Visa, Mastercard, PayPal, and Apple Pay. All payments are processed securely. You can save multiple payment methods to your account.", Tags: []string{"payment", "methods"}},
		{ID: "ord_01", Title: "Order Tracking", Content: "You can track your order from the Orders page. You will receive a tracking number via email once your order ships. Check the carrier's website for real-time updates.", Tags: []string{"order", "tracking", "shipping"}},
	},
}

// RAGInput is the input for the RAG retrieval tool.
type RAGInput struct {
	Query string `json:"query" jsonschema:"-" description:"The search query"`
}

// RAGSearchTool creates a tool that searches the knowledge base and synthesizes
// an answer using the given LLM.
func RAGSearchTool(llm model.BaseChatModel) (tool.InvokableTool, error) {
	return utils.InferTool[*RAGInput, string](
		"knowledge_search",
		"Search the knowledge base for information related to the query. "+
			"Returns a synthesized answer with source references.",
		func(ctx context.Context, input *RAGInput) (string, error) {
			if input.Query == "" {
				return "", fmt.Errorf("empty query")
			}

			// Step 1: Retrieve relevant documents (keyword-based search for now)
			docs := globalKB.search(input.Query)

			if len(docs) == 0 {
				return `{"answer":"I couldn't find relevant information in the knowledge base.","sources":[],"relevant":false}`, nil
			}

			// Step 2: Re-rank by relevance and limit context
			scored := rankDocs(input.Query, docs)
			topDocs := scored
			if len(topDocs) > 3 {
				topDocs = topDocs[:3]
			}

			// Step 3: Build context from top documents
			var contextBuilder strings.Builder
			var sources []string
			for _, d := range topDocs {
				contextBuilder.WriteString(fmt.Sprintf("Title: %s\nContent: %s\n\n", d.doc.Title, d.doc.Content))
				sources = append(sources, d.doc.ID)
			}

			// Step 4: Synthesize answer using LLM
			answer, err := synthesizeAnswer(ctx, llm, input.Query, contextBuilder.String())
			if err != nil {
				answer = contextBuilder.String()
			}

			srcJSON := `"` + strings.Join(sources, `","`) + `"`
			return fmt.Sprintf(`{"answer":"%s","sources":[%s],"relevant":true}`,
				escapeJSON(answer), srcJSON), nil
		},
	)
}

// search performs simple keyword-based search over the knowledge base.
func (kb *knowledgeBase) search(query string) []KnowledgeDoc {
	kb.mu.RLock()
	defer kb.mu.RUnlock()

	queryLower := strings.ToLower(query)
	terms := strings.Fields(queryLower)

	var results []KnowledgeDoc
	for _, doc := range kb.docs {
		contentLower := strings.ToLower(doc.Content)
		titleLower := strings.ToLower(doc.Title)
		matchCount := 0
		for _, term := range terms {
			if strings.Contains(contentLower, term) || strings.Contains(titleLower, term) {
				matchCount++
			}
		}
		if matchCount > 0 {
			results = append(results, doc)
		}
	}
	return results
}

type scoredDoc struct {
	doc   KnowledgeDoc
	score int
}

// rankDocs scores documents by term frequency, title matches weighted higher.
func rankDocs(query string, docs []KnowledgeDoc) []scoredDoc {
	terms := strings.Fields(strings.ToLower(query))
	var scored []scoredDoc
	for _, doc := range docs {
		score := 0
		contentLower := strings.ToLower(doc.Content)
		titleLower := strings.ToLower(doc.Title)
		for _, term := range terms {
			score += strings.Count(contentLower, term)
			score += strings.Count(titleLower, term) * 2
		}
		scored = append(scored, scoredDoc{doc: doc, score: score})
	}

	for i := 1; i < len(scored); i++ {
		key := scored[i]
		j := i - 1
		for j >= 0 && scored[j].score < key.score {
			scored[j+1] = scored[j]
			j--
		}
		scored[j+1] = key
	}
	return scored
}

// synthesizeAnswer uses the LLM to generate a natural answer from documents.
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
