package memory

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LongTermMemory provides persistent long-term storage for user sessions.
// Data is stored per-user in JSON files under the configured directory.
type LongTermMemory struct {
	mu       sync.RWMutex
	basePath string
	users    map[string][]LongTermEntry
}

// LongTermEntry represents a persisted memory entry.
type LongTermEntry struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
	Time    string   `json:"time"`
}

// NewLongTermMemory creates long-term storage under the given directory.
func NewLongTermMemory(basePath string) (*LongTermMemory, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, err
	}
	return &LongTermMemory{
		basePath: basePath,
		users:    make(map[string][]LongTermEntry),
	}, nil
}

// Append saves an entry for a user.
func (ltm *LongTermMemory) Append(userID, content string, tags []string) error {
	ltm.mu.Lock()
	defer ltm.mu.Unlock()

	entry := LongTermEntry{
		ID:      userID + "_" + truncate(content, 20),
		Content: content,
		Tags:    tags,
		Time:    filepath.Base(userID),
	}
	ltm.users[userID] = append(ltm.users[userID], entry)
	return ltm.persistUser(userID)
}

// Search returns up to topK entries matching the query via keyword similarity.
func (ltm *LongTermMemory) Search(userID, query string, topK int) []LongTermEntry {
	ltm.mu.RLock()
	defer ltm.mu.RUnlock()

	entries := ltm.users[userID]
	if len(entries) == 0 {
		return nil
	}

	queryTerms := tokenize(query)
	type scored struct {
		entry LongTermEntry
		score float64
	}
	var scoredEntries []scored

	for _, e := range entries {
		score := cosineSimilarity(queryTerms, tokenize(e.Content))
		for _, tag := range e.Tags {
			for qt := range queryTerms {
				if strings.Contains(strings.ToLower(tag), qt) {
					score += 0.3
				}
			}
		}
		if score > 0 {
			scoredEntries = append(scoredEntries, scored{e, score})
		}
	}

	// Sort by score descending.
	for i := 1; i < len(scoredEntries); i++ {
		key := scoredEntries[i]
		j := i - 1
		for j >= 0 && scoredEntries[j].score < key.score {
			scoredEntries[j+1] = scoredEntries[j]
			j--
		}
		scoredEntries[j+1] = key
	}

	if topK <= 0 || topK > len(scoredEntries) {
		topK = len(scoredEntries)
	}
	result := make([]LongTermEntry, topK)
	for i := 0; i < topK; i++ {
		result[i] = scoredEntries[i].entry
	}
	return result
}

func (ltm *LongTermMemory) persistUser(userID string) error {
	data, err := json.Marshal(ltm.users[userID])
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ltm.basePath, userID+".json"), data, 0644)
}

func tokenize(s string) map[string]float64 {
	words := strings.Fields(strings.ToLower(s))
	tf := make(map[string]float64)
	for _, w := range words {
		tf[w]++
	}
	total := float64(len(words))
	if total > 0 {
		for k, v := range tf {
			tf[k] = v / total
		}
	}
	return tf
}

func cosineSimilarity(a, b map[string]float64) float64 {
	var dot, normA, normB float64
	for k, v := range a {
		dot += v * b[k]
		normA += v * v
	}
	for _, v := range b {
		normB += v * v
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func truncate(s string, maxLen int) string {
	cleaned := strings.ReplaceAll(strings.ReplaceAll(s, " ", "_"), "\n", "_")
	if len(cleaned) > maxLen {
		return cleaned[:maxLen]
	}
	return cleaned
}
