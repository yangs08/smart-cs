package agent

import (
	"context"
	"fmt"

	"helpdesk-agent/agent/helpdesk"
	"helpdesk-agent/knowledge"
	"helpdesk-agent/llm"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// HelpDeskConfig holds the dependencies for building the help desk supervisor.
type HelpDeskConfig struct {
	Router *llm.Router
	KB     *knowledge.HybridStore
}

// BuildSupervisor creates the complete help desk supervisor with all
// sub-agents and tools registered.
func BuildSupervisor(ctx context.Context, cfg *HelpDeskConfig) (adk.ResumableAgent, error) {
	// 1. Build Lambda tools registered directly on the Supervisor.
	intentTool, err := helpdesk.IntentClassifyTool(cfg.Router.Select(llm.RoleFast))
	if err != nil {
		return nil, fmt.Errorf("build intent_classify tool: %w", err)
	}

	complianceTool, err := helpdesk.ComplianceCheckTool(cfg.Router.Select(llm.RoleFast))
	if err != nil {
		return nil, fmt.Errorf("build compliance_check tool: %w", err)
	}

	// 2. Build knowledge RAG sub-agent.
	ragTool, err := helpdesk.RAGSearchTool(cfg.Router.Select(llm.RoleReasoning), cfg.KB)
	if err != nil {
		return nil, fmt.Errorf("build RAG tool: %w", err)
	}

	ragAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "knowledge_rag",
		Description: "Searches the knowledge base for information about shipping, returns, payments, orders, and account management.",
		Model:       cfg.Router.Select(llm.RoleDefault),
		Instruction: `You are a knowledge base search agent.
Your only tool is "knowledge_search" — use it to answer questions.
When you receive the result, summarize the answer for the user.
If the search returns no relevant results, say so politely.
After providing the answer, transfer back to the supervisor.`,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{ragTool},
			},
		},
		MaxIterations: 5,
	})
	if err != nil {
		return nil, fmt.Errorf("build knowledge_rag agent: %w", err)
	}

	// 3. Build ticket handler sub-agent.
	createTicket, err := helpdesk.CreateTicketTool()
	if err != nil {
		return nil, fmt.Errorf("build create_ticket tool: %w", err)
	}
	getTicket, err := helpdesk.GetTicketTool()
	if err != nil {
		return nil, fmt.Errorf("build get_ticket tool: %w", err)
	}
	updateTicket, err := helpdesk.UpdateTicketTool()
	if err != nil {
		return nil, fmt.Errorf("build update_ticket tool: %w", err)
	}

	ticketAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "ticket_handler",
		Description: "Creates, retrieves, and updates support tickets.",
		Model:       cfg.Router.Select(llm.RoleDefault),
		Instruction: `You are a ticket handling agent.
Available tools:
- create_ticket: Create a new ticket (requires customer_id, description, optional priority)
- get_ticket: Look up a ticket by ID
- update_ticket: Update a ticket's status, description, or priority

When creating a ticket, ask for any missing required information.
After completing the operation, transfer back to the supervisor.`,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{createTicket, getTicket, updateTicket},
			},
		},
		MaxIterations: 5,
	})
	if err != nil {
		return nil, fmt.Errorf("build ticket_handler agent: %w", err)
	}

	// 4. Wrap sub-agents with deterministic transfer back to supervisor.
	subAgents := []adk.Agent{
		adk.AgentWithDeterministicTransferTo(ctx, &adk.DeterministicTransferConfig{
			Agent:        ragAgent,
			ToAgentNames: []string{"help_desk_supervisor"},
		}),
		adk.AgentWithDeterministicTransferTo(ctx, &adk.DeterministicTransferConfig{
			Agent:        ticketAgent,
			ToAgentNames: []string{"help_desk_supervisor"},
		}),
	}

	// 5. Create supervisor agent.
	supervisor, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:  "help_desk_supervisor",
		Model: cfg.Router.Select(llm.RoleDefault),
		Instruction: `You are a customer service supervisor agent.

TOOLS (lightweight functions, call directly):
- intent_classify: Classify user query intent (call first)
- compliance_check: Check content for PII and rule violations

SUB-AGENTS (transfer when needed):
- knowledge_rag: For questions about shipping, returns, payments, orders, or account help
- ticket_handler: For creating, updating, or checking support tickets

WORKFLOW:
1. Call intent_classify to understand the user's intent
2. Based on the result:
   - Greeting / simple questions -> reply directly
   - Knowledge/inquiry questions -> transfer to knowledge_rag
   - Ticket operations -> transfer to ticket_handler
   - Compliance-sensitive content -> call compliance_check
3. Synthesize the final response for the user

EXAMPLES:
User: "我的订单怎么还没到" -> intent_classify -> order_inquiry -> transfer to knowledge_rag
User: "我要退款" -> intent_classify -> refund_request -> transfer to ticket_handler
User: "你好" -> intent_classify -> greeting -> reply directly`,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{intentTool, complianceTool},
			},
		},
		MaxIterations: 20,
	})
	if err != nil {
		return nil, fmt.Errorf("build supervisor: %w", err)
	}

	// 6. Register sub-agents.
	return adk.SetSubAgents(ctx, supervisor, subAgents)
}
