package helpdesk

// IntentResult holds the classification result from intent_classify.
type IntentResult struct {
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	SubIntent  string  `json:"sub_intent"`
}

// Document represents a retrieved knowledge document from RAG.
type Document struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
	Source  string  `json:"source"`
}

// TicketInfo holds the context of a support ticket.
type TicketInfo struct {
	TicketID    string `json:"ticket_id"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	CustomerID  string `json:"customer_id"`
}

// ComplianceResult holds the result of a compliance check.
type ComplianceResult struct {
	IsCompliant      bool     `json:"is_compliant"`
	Violations       []string `json:"violations"`
	PIIFound         bool     `json:"pii_found"`
	SanitizedContent string   `json:"sanitized_content"`
	VerifiedByLLM    bool     `json:"verified_by_llm"`
}

// CustomerSvcState is the global state contract for the customer service system.
// Values are passed across agents via ADK SessionValues (key-value).
// This struct defines the type contract — see SessionValues keys in each agent.
type CustomerSvcState struct {
	CurrentIntent     *IntentResult     `json:"current_intent"`
	RetrievedContexts []*Document       `json:"retrieved_contexts"`
	TicketData        *TicketInfo       `json:"ticket_data"`
	ComplianceFlags   *ComplianceResult `json:"compliance_flags"`
}

// SessionValues keys used across agents
const (
	SVCurrentIntent     = "current_intent"
	SVRetrievedContexts  = "retrieved_contexts"
	SVTicketData        = "ticket_data"
	SVComplianceFlags   = "compliance_flags"
)
