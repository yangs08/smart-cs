package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	rrfK   = 60
)

// ScoredDoc is a document with its relevance score.
type ScoredDoc struct {
	Doc   Doc
	Score float64
}

// HybridStore combines vector search (Qdrant) + BM25 rerank + RRF fusion.
type HybridStore struct {
	// BM25 corpus stats (loaded from file at startup)
	avgDocLen float64
	totalDocs int
	nTerms    map[string]int

	// Persistence
	statsPath string

	// Qdrant REST client
	qdrantURL  string
	collection string
	httpClient *http.Client

	// Embedding
	embedder Embedder

	mu sync.RWMutex
}

// NewHybridStore creates a HybridStore. Documents are stored in Qdrant, not in memory.
// BM25 stats can be loaded later via LoadStats() or SetStatsPath().
func NewHybridStore(qdrantAddr, collection string, embedder Embedder) (*HybridStore, error) {
	return &HybridStore{
		qdrantURL:  fmt.Sprintf("http://%s", qdrantAddr),
		collection: collection,
		httpClient: &http.Client{},
		embedder:   embedder,
	}, nil
}

// SetStatsPath sets the path for persisting/loading BM25 stats.
func (s *HybridStore) SetStatsPath(path string) {
	s.statsPath = path
}

// LoadStats loads BM25 corpus statistics from a JSON file.
func (s *HybridStore) LoadStats(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read bm25 stats: %w", err)
	}
	var stats BM25Stats
	if err := json.Unmarshal(data, &stats); err != nil {
		return fmt.Errorf("unmarshal bm25 stats: %w", err)
	}
	s.mu.Lock()
	s.avgDocLen = stats.AvgDocLen
	s.totalDocs = stats.TotalDocs
	s.nTerms = stats.NTerms
	s.mu.Unlock()
	return nil
}

// Search performs two-stage hybrid search:
//  1. Vector search in Qdrant (retrieves topK*3 candidates with full content)
//  2. BM25 rerank on candidates (if BM25 stats loaded)
//  3. RRF fusion → final topK results
func (s *HybridStore) Search(ctx context.Context, query string, topK int) ([]ScoredDoc, bool, error) {
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, false, fmt.Errorf("embed query: %w", err)
	}

	// Stage 1: Vector search (retrieve extra candidates for rerank buffer)
	vecResults, err := s.vectorSearch(ctx, embedding, topK*3)
	if err != nil {
		return nil, false, fmt.Errorf("vector search: %w", err)
	}
	if len(vecResults) == 0 {
		return nil, false, nil
	}

	hasBM25 := s.avgDocLen > 0 && len(s.nTerms) > 0

	// Prepare RRF entries from vector results
	type rrfEntry struct {
		doc      Doc
		vecRank  int
		bm25Rank int
	}
	entries := make([]rrfEntry, len(vecResults))
	for i, sd := range vecResults {
		entries[i] = rrfEntry{
			doc:     sd.Doc,
			vecRank: i + 1,
		}
	}

	// Stage 2: BM25 rerank on vector candidates
	if hasBM25 {
		queryTerms := tokenize(query)
		scored := make([]struct {
			idx   int
			score float64
		}, len(entries))
		for i, e := range entries {
			docLen := len(strings.Fields(e.doc.Content))
			scored[i] = struct {
				idx   int
				score float64
			}{i, s.bm25SingleScore(docLen, e.doc.Content+" "+e.doc.Title, queryTerms)}
		}
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].score > scored[j].score
		})
		for rank, sv := range scored {
			if sv.score > 0 {
				entries[sv.idx].bm25Rank = rank + 1
			}
		}
	}

	// Stage 3: RRF fusion
	results := make([]ScoredDoc, len(entries))
	for i, e := range entries {
		score := 1.0 / float64(rrfK+e.vecRank)
		if hasBM25 && e.bm25Rank > 0 {
			score += 1.0 / float64(rrfK+e.bm25Rank)
		}
		results[i] = ScoredDoc{
			Doc:   e.doc,
			Score: math.Round(score*10000) / 10000,
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}

	hasResult := len(results) > 0 && results[0].Score > 0.005
	return results, hasResult, nil
}

// bm25SingleScore computes BM25 score for a single document using global corpus stats.
func (s *HybridStore) bm25SingleScore(docLen int, content string, queryTerms map[string]bool) float64 {
	n := s.totalDocs
	docTerms := make(map[string]int)
	for _, t := range strings.Fields(strings.ToLower(content)) {
		docTerms[t]++
	}

	var score float64
	for t := range queryTerms {
		tf := float64(docTerms[t])
		if tf == 0 {
			continue
		}
		nqt := float64(s.nTerms[t])
		idf := math.Log(1 + (float64(n)-nqt+0.5)/(nqt+0.5))
		if idf < 0 {
			idf = 0
		}
		numer := tf * (bm25K1 + 1)
		denom := tf + bm25K1*(1-bm25B+bm25B*float64(docLen)/s.avgDocLen)
		score += idf * numer / denom
	}
	return score
}

// ----- Qdrant REST API -----

func (s *HybridStore) initCollection(ctx context.Context) error {
	collURL := fmt.Sprintf("%s/collections/%s", s.qdrantURL, s.collection)

	req, _ := http.NewRequestWithContext(ctx, "GET", collURL, nil)
	resp, err := s.httpClient.Do(req)
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		return nil // already exists
	}
	if resp != nil {
		resp.Body.Close()
	}

	type createCollReq struct {
		Vectors struct {
			Size     int    `json:"size"`
			Distance string `json:"distance"`
		} `json:"vectors"`
	}
	var body createCollReq
	body.Vectors.Size = 768
	body.Vectors.Distance = "Cosine"

	data, _ := json.Marshal(body)
	req, _ = http.NewRequestWithContext(ctx, "PUT", collURL, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	resp, err = s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant create collection: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant create collection: %s", string(b))
	}
	return nil
}

func (s *HybridStore) upsertDocs(ctx context.Context, docs []Doc, embeddings [][]float64) error {
	pointsURL := fmt.Sprintf("%s/collections/%s/points", s.qdrantURL, s.collection)

	type qdrantPoint struct {
		ID      uint64          `json:"id"`
		Vector  []float64       `json:"vector"`
		Payload json.RawMessage `json:"payload"`
	}

	type upsertReq struct {
		Points []json.RawMessage `json:"points"`
		Wait   bool              `json:"wait"`
	}
	var points []json.RawMessage

	for i, d := range docs {
		payload, _ := json.Marshal(map[string]interface{}{
			"doc_id":  d.ID,
			"title":   d.Title,
			"content": d.Content,
			"tags":    d.Tags,
		})
		vec := make([]float64, len(embeddings[i]))
		copy(vec, embeddings[i])
		pt, _ := json.Marshal(qdrantPoint{
			ID:      hashID(d.ID),
			Vector:  vec,
			Payload: payload,
		})
		points = append(points, pt)
	}

	body := upsertReq{Points: points, Wait: true}
	data, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "PUT", pointsURL, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant upsert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant upsert: %s", string(b))
	}
	return nil
}

func (s *HybridStore) vectorSearch(ctx context.Context, vec []float64, limit int) ([]ScoredDoc, error) {
	searchURL := fmt.Sprintf("%s/collections/%s/points/search", s.qdrantURL, s.collection)

	qvec := make([]float64, len(vec))
	copy(qvec, vec)

	body := struct {
		Vector      []float64 `json:"vector"`
		Limit       int       `json:"limit"`
		WithPayload bool      `json:"with_payload"`
	}{Vector: qvec, Limit: limit, WithPayload: true}

	data, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant search: %s", string(b))
	}

	var result struct {
		Result []struct {
			ID      uint64          `json:"id"`
			Score   float64         `json:"score"`
			Payload json.RawMessage `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	scored := make([]ScoredDoc, 0, len(result.Result))
	for _, r := range result.Result {
		var payload struct {
			DocID   string   `json:"doc_id"`
			Title   string   `json:"title"`
			Content string   `json:"content"`
			Tags    []string `json:"tags"`
		}
		if err := json.Unmarshal(r.Payload, &payload); err != nil {
			continue
		}
		scored = append(scored, ScoredDoc{
			Doc: Doc{
				ID:      payload.DocID,
				Title:   payload.Title,
				Content: payload.Content,
				Tags:    payload.Tags,
			},
			Score: math.Round(r.Score*100) / 100,
		})
	}
	return scored, nil
}

// ----- DefaultDocs -----

func DefaultDocs() []Doc {
	return []Doc{
		{ID: "ship_01", Title: "Shipping Policy", Content: "Standard shipping takes 3-5 business days. Express shipping takes 1-2 business days. Free shipping on orders over $50.", Tags: []string{"shipping", "delivery", "policy"}},
		{ID: "ret_01", Title: "Return Policy", Content: "Items can be returned within 30 days of delivery. Items must be unused and in original packaging. Refund will be processed within 5-7 business days after we receive the return.", Tags: []string{"return", "refund", "policy"}},
		{ID: "acc_01", Title: "Account Management", Content: "You can reset your password from the login page. To update your profile, go to Settings > Profile. For security, enable two-factor authentication.", Tags: []string{"account", "password", "security"}},
		{ID: "pay_01", Title: "Payment Methods", Content: "We accept Visa, Mastercard, PayPal, and Apple Pay. All payments are processed securely. You can save multiple payment methods to your account.", Tags: []string{"payment", "methods"}},
		{ID: "ord_01", Title: "Order Tracking", Content: "You can track your order from the Orders page. You will receive a tracking number via email once your order ships. Check the carrier's website for real-time updates.", Tags: []string{"order", "tracking", "shipping"}},
	}
}

// ----- helpers -----

func hashID(id string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(id); i++ {
		h ^= uint64(id[i])
		h *= 1099511628211
	}
	return h
}

func tokenize(s string) map[string]bool {
	words := strings.Fields(strings.ToLower(s))
	set := make(map[string]bool, len(words))
	for _, w := range words {
		if len(w) > 1 {
			set[strings.Trim(w, ".,!?;:\"'()[]{}")] = true
		}
	}
	return set
}

func collectTerms(s string) map[string]struct{} {
	words := strings.Fields(strings.ToLower(s))
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		if len(w) > 1 {
			set[strings.Trim(w, ".,!?;:\"'()[]{}")] = struct{}{}
		}
	}
	return set
}
