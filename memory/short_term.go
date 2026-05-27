package memory

import (
	"fmt"
	"sync"
)

// ShortTermMemory provides short-term session storage.
// Uses an in-memory store by default.
type ShortTermMemory struct {
	mu         sync.RWMutex
	sessions   map[string][]SessionEntry
	maxEntries int
}

// SessionEntry represents a single exchange in a session.
type SessionEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// NewShortTermMemory creates a short-term memory store.
// maxEntries controls entries per session before older ones are summarized.
func NewShortTermMemory(maxEntries int) *ShortTermMemory {
	if maxEntries <= 0 {
		maxEntries = 20
	}
	return &ShortTermMemory{
		sessions:   make(map[string][]SessionEntry),
		maxEntries: maxEntries,
	}
}

// Append adds an entry to a session's history.
func (stm *ShortTermMemory) Append(sessionID, role, content string) {
	stm.mu.Lock()
	defer stm.mu.Unlock()

	stm.sessions[sessionID] = append(stm.sessions[sessionID],
		SessionEntry{Role: role, Content: content})

	if stm.maxEntries > 0 && len(stm.sessions[sessionID]) > stm.maxEntries {
		entries := stm.sessions[sessionID]
		trimCount := len(entries) - stm.maxEntries
		summary := fmt.Sprintf("[Earlier %d messages summarized]", trimCount)
		stm.sessions[sessionID] = append(
			[]SessionEntry{{Role: "system", Content: summary}},
			entries[trimCount:]...,
		)
	}
}

// GetHistory returns all entries for a session.
func (stm *ShortTermMemory) GetHistory(sessionID string) []SessionEntry {
	stm.mu.RLock()
	defer stm.mu.RUnlock()
	entries := stm.sessions[sessionID]
	result := make([]SessionEntry, len(entries))
	copy(result, entries)
	return result
}

// ClearSession removes all entries for a session.
func (stm *ShortTermMemory) ClearSession(sessionID string) {
	stm.mu.Lock()
	defer stm.mu.Unlock()
	delete(stm.sessions, sessionID)
}

// SessionCount returns the number of active sessions.
func (stm *ShortTermMemory) SessionCount() int {
	stm.mu.RLock()
	defer stm.mu.RUnlock()
	return len(stm.sessions)
}
