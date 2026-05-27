package memory

import "sync"

// WorkingMemory provides an in-memory session-level cache.
// Safe for concurrent use. Not persisted.
type WorkingMemory struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewWorkingMemory creates a new working memory store.
func NewWorkingMemory() *WorkingMemory {
	return &WorkingMemory{
		data: make(map[string]any),
	}
}

// Set stores a value by key.
func (wm *WorkingMemory) Set(key string, value any) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.data[key] = value
}

// Get retrieves a value by key.
func (wm *WorkingMemory) Get(key string) (any, bool) {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	val, ok := wm.data[key]
	return val, ok
}

// Delete removes a key.
func (wm *WorkingMemory) Delete(key string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	delete(wm.data, key)
}

// Clear removes all entries.
func (wm *WorkingMemory) Clear() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.data = make(map[string]any)
}

// Size returns the number of entries.
func (wm *WorkingMemory) Size() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return len(wm.data)
}
