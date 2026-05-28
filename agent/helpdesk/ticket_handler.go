package helpdesk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ticketRecord is an internal representation of a support ticket.
type ticketRecord struct {
	TicketID    string
	CustomerID  string
	Description string
	Status      string
	Priority    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ticketStore is a thread-safe in-memory ticket store.
type ticketStore struct {
	mu      sync.RWMutex
	tickets map[string]*ticketRecord
	counter int
}

var globalTickets = &ticketStore{
	tickets: make(map[string]*ticketRecord),
}

// CreateTicketInput is the input for the create_ticket tool.
type CreateTicketInput struct {
	CustomerID  string `json:"customer_id" description:"The customer ID"`
	Description string `json:"description" description:"The issue description"`
	Priority    string `json:"priority" description:"Priority: low, medium, high, urgent"`
}

// GetTicketInput is the input for the get_ticket tool.
type GetTicketInput struct {
	TicketID string `json:"ticket_id" description:"The ticket ID to look up"`
}

// UpdateTicketInput is the input for the update_ticket tool.
type UpdateTicketInput struct {
	TicketID    string `json:"ticket_id" description:"The ticket ID"`
	Description string `json:"description,omitempty" description:"Updated description"`
	Status      string `json:"status,omitempty" description:"open, in_progress, resolved, closed"`
	Priority    string `json:"priority,omitempty" description:"low, medium, high, urgent"`
}

// CreateTicketTool creates a tool that creates a new support ticket.
func CreateTicketTool() (tool.InvokableTool, error) {
	return utils.InferTool[*CreateTicketInput, TicketResult](
		"create_ticket",
		"Create a new support ticket. Returns the ticket details.",
		func(ctx context.Context, input *CreateTicketInput) (TicketResult, error) {
			if input.CustomerID == "" || input.Description == "" {
				return TicketResult{}, fmt.Errorf("customer_id and description are required")
			}
			if input.Priority == "" {
				input.Priority = "medium"
			}

			record := globalTickets.create(input.CustomerID, input.Description, input.Priority)
			return TicketResult{
				TicketID:   record.TicketID,
				Status:     record.Status,
				Priority:   record.Priority,
				CustomerID: record.CustomerID,
			}, nil
		},
	)
}

// GetTicketTool creates a tool that retrieves a ticket by ID.
func GetTicketTool() (tool.InvokableTool, error) {
	return utils.InferTool[*GetTicketInput, TicketResult](
		"get_ticket",
		"Get a support ticket by ID. Returns ticket details.",
		func(ctx context.Context, input *GetTicketInput) (TicketResult, error) {
			if input.TicketID == "" {
				return TicketResult{}, fmt.Errorf("ticket_id is required")
			}

			record, ok := globalTickets.get(input.TicketID)
			if !ok {
				return TicketResult{
					TicketID: input.TicketID,
					Error:    "ticket not found",
				}, nil
			}

			return TicketResult{
				TicketID:    record.TicketID,
				Status:      record.Status,
				Priority:    record.Priority,
				CustomerID:  record.CustomerID,
				Description: record.Description,
				CreatedAt:   record.CreatedAt.Format(time.RFC3339),
			}, nil
		},
	)
}

// UpdateTicketTool creates a tool that updates an existing ticket.
func UpdateTicketTool() (tool.InvokableTool, error) {
	return utils.InferTool[*UpdateTicketInput, TicketResult](
		"update_ticket",
		"Update a support ticket's status, description, or priority. Returns updated ticket.",
		func(ctx context.Context, input *UpdateTicketInput) (TicketResult, error) {
			if input.TicketID == "" {
				return TicketResult{}, fmt.Errorf("ticket_id is required")
			}

			_, ok := globalTickets.get(input.TicketID)
			if !ok {
				return TicketResult{
					TicketID: input.TicketID,
					Error:    "ticket not found",
				}, nil
			}

			globalTickets.update(input.TicketID, func(r *ticketRecord) {
				if input.Description != "" {
					r.Description = input.Description
				}
				if input.Status != "" {
					r.Status = input.Status
				}
				if input.Priority != "" {
					r.Priority = input.Priority
				}
				r.UpdatedAt = time.Now()
			})

			record, _ := globalTickets.get(input.TicketID)
			return TicketResult{
				TicketID:    record.TicketID,
				Status:      record.Status,
				Priority:    record.Priority,
				CustomerID:  record.CustomerID,
				Description: record.Description,
			}, nil
		},
	)
}

func (ts *ticketStore) create(customerID, description, priority string) *ticketRecord {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.counter++
	ticketID := fmt.Sprintf("TKT-%05d", ts.counter)
	now := time.Now()
	record := &ticketRecord{
		TicketID:    ticketID,
		CustomerID:  customerID,
		Description: description,
		Status:      "open",
		Priority:    priority,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	ts.tickets[ticketID] = record
	return record
}

func (ts *ticketStore) get(ticketID string) (*ticketRecord, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	record, ok := ts.tickets[ticketID]
	return record, ok
}

func (ts *ticketStore) update(ticketID string, fn func(*ticketRecord)) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if record, ok := ts.tickets[ticketID]; ok {
		fn(record)
	}
}
