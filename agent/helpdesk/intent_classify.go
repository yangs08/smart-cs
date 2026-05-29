package helpdesk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// IntentClassifyInput is the input for the intent classification tool.
type IntentClassifyInput struct {
	Query string `json:"query" description:"The user's query to classify"`
}

// keywordCategory maps keywords to intent categories for fast O(1) matching.
var keywordCategory = map[string]string{
	"订单": "order_inquiry", "物流": "order_inquiry", "快递": "order_inquiry",
	"配送": "order_inquiry", "发货": "order_inquiry", "到哪了": "order_inquiry",
	"还没到": "order_inquiry", "订单状态": "order_inquiry", "我的订单": "order_inquiry",

	"退款": "refund_request", "退货": "refund_request", "退换": "refund_request",
	"取消订单": "refund_request", "不想要了": "refund_request", "退钱": "refund_request",

	"登录": "account_issue", "账号": "account_issue", "密码": "account_issue",
	"账户": "account_issue", "注册": "account_issue",

	"投诉": "complaint", "差评": "complaint", "态度差": "complaint",
	"不满意": "complaint", "质量问题": "complaint",

	"商品": "product_inquiry", "介绍": "product_inquiry", "多少钱": "product_inquiry",
	"价格": "product_inquiry", "有货吗": "product_inquiry", "库存": "product_inquiry",
	"推荐": "product_inquiry",

	"你好": "greeting", "您好": "greeting", "hi": "greeting", "hello": "greeting",

	"帮助": "knowledge_base", "怎么": "knowledge_base", "如何": "knowledge_base",
	"什么": "knowledge_base", "为什么": "knowledge_base", "说明": "knowledge_base",
	"教程": "knowledge_base",
}

// IntentClassifyTool creates a tool that classifies user intent using keyword
// matching with LLM fallback for ambiguous queries.
func IntentClassifyTool(llm model.BaseChatModel) (tool.InvokableTool, error) {
	return utils.InferTool[*IntentClassifyInput, IntentResult](
		"intent_classify",
		"Classify the user's query into an intent category.",
		func(ctx context.Context, input *IntentClassifyInput) (IntentResult, error) {
			if input.Query == "" {
				return IntentResult{}, fmt.Errorf("empty query")
			}

			result := classifyByKeywords(input.Query)
			if result != nil {
				return *result, nil
			}

			result, err := classifyByLLM(ctx, llm, input.Query)
			if err != nil {
				return IntentResult{}, fmt.Errorf("llm classification failed: %w", err)
			}

			return *result, nil
		},
	)
}

// classifyByKeywords attempts to classify the query using keyword matching.
func classifyByKeywords(query string) *IntentResult {
	for kw, cat := range keywordCategory {
		if strings.Contains(query, kw) {
			return &IntentResult{
				Category:   cat,
				Confidence: 0.85,
				SubIntent:  cat,
			}
		}
	}
	return nil
}

// classifyByLLM uses an LLM call for ambiguous queries that keyword matching
// couldn't classify.
func classifyByLLM(ctx context.Context, llm model.BaseChatModel, query string) (*IntentResult, error) {
	sysPrompt := schema.SystemMessage(`You are an intent classifier for a customer service system.
Classify the user's query into one of: order_inquiry, refund_request, account_issue,
complaint, product_inquiry, greeting, knowledge_base, other.

Respond with ONLY a JSON object:
{"category":"...","confidence":0.0-1.0,"sub_intent":"..."}`)

	prompt := schema.UserMessage(query)

	resp, err := llm.Generate(ctx, []*schema.Message{sysPrompt, prompt})
	if err != nil {
		return nil, err
	}

	return parseLLMResponse(resp.Content)
}

// parseLLMResponse extracts IntentResult from LLM JSON response.
func parseLLMResponse(content string) (*IntentResult, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result IntentResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %s", content)
	}
	return &result, nil
}
