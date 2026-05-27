package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	rrfK   = 60
)

type Doc struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

type ScoredDoc struct {
	Doc   Doc
	Score float64
}

// HybridStore combines BM25 keyword search + vector search + RRF fusion.
type HybridStore struct {
	docs      []Doc
	avgDocLen float64
	nTerms    map[string]int // term -> number of docs containing it

	// Qdrant REST client
	qdrantURL  string
	collection string
	httpClient *http.Client

	// Embedding
	embedder Embedder

	mu sync.RWMutex
}

func NewHybridStore(qdrantAddr, collection string, embedder Embedder, docs []Doc) (*HybridStore, error) {
	if len(docs) == 0 {
		return nil, fmt.Errorf("knowledge: at least one document required")
	}

	// Precompute BM25 stats
	totalLen := 0
	nTerms := make(map[string]int)
	for _, d := range docs {
		totalLen += len(tokenize(d.Content + " " + d.Title))
		terms := collectTerms(d.Content + " " + d.Title)
		for t := range terms {
			nTerms[t]++
		}
	}

	s := &HybridStore{
		docs:       docs,
		avgDocLen:  math.Max(float64(totalLen)/float64(len(docs)), 1),
		nTerms:     nTerms,
		qdrantURL:  fmt.Sprintf("http://%s", qdrantAddr),
		collection: collection,
		httpClient: &http.Client{},
		embedder:   embedder,
	}

	return s, nil
}

// Search performs hybrid search: BM25 + vector + RRF fusion, returns topK.
func (s *HybridStore) Search(ctx context.Context, query string, topK int) ([]ScoredDoc, bool, error) {
	n := len(s.docs)
	if n == 0 {
		return nil, false, nil
	}

	// 1. BM25 retrieval
	bm25Results := s.bm25Search(query, topK*2)
	var bm25Set map[string]int
	for i, sd := range bm25Results {
		if bm25Set == nil {
			bm25Set = make(map[string]int)
		}
		bm25Set[sd.Doc.ID] = i + 1
	}

	// 2. Vector retrieval (best-effort)
	var vectorResults []ScoredDoc
	var vectorSet map[string]int
	embedding, err := s.embedder.Embed(ctx, query)
	if err == nil {
		vectorResults, _ = s.vectorSearch(embedding, topK*2, bm25Results)
		for i, sd := range vectorResults {
			if vectorSet == nil {
				vectorSet = make(map[string]int)
			}
			vectorSet[sd.Doc.ID] = i + 1
		}
	}

	// 3. RRF fusion
	type rrfEntry struct {
		doc   Doc
		score float64
	}
	fused := make(map[string]*rrfEntry)

	for _, sd := range bm25Results {
		fused[sd.Doc.ID] = &rrfEntry{doc: sd.Doc}
	}
	for _, sd := range vectorResults {
		if _, ok := fused[sd.Doc.ID]; !ok {
			fused[sd.Doc.ID] = &rrfEntry{doc: sd.Doc}
		}
	}

	for id, entry := range fused {
		if rank, ok := bm25Set[id]; ok {
			entry.score += 1.0 / float64(rrfK+rank)
		}
		if rank, ok := vectorSet[id]; ok {
			entry.score += 1.0 / float64(rrfK+rank)
		}
	}

	results := make([]ScoredDoc, 0, len(fused))
	for _, entry := range fused {
		results = append(results, ScoredDoc{
			Doc:   entry.doc,
			Score: math.Round(entry.score*10000) / 10000,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}

	// Check confidence: if top result score is below threshold, return empty
	hasResult := len(results) > 0 && results[0].Score > 0.005
	return results, hasResult, nil
}

// ----- BM25 -----

func (s *HybridStore) bm25Search(query string, topK int) []ScoredDoc {
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return nil
	}

	type scored struct {
		doc   Doc
		score float64
	}
	var all []scored

	for _, d := range s.docs {
		score := s.bm25Score(d, queryTerms)
		if score > 0 {
			all = append(all, scored{d, score})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].score > all[j].score
	})
	if len(all) > topK {
		all = all[:topK]
	}

	result := make([]ScoredDoc, len(all))
	for i, s := range all {
		result[i] = ScoredDoc{
			Doc:   s.doc,
			Score: math.Round(s.score*100) / 100,
		}
	}
	return result
}

func (s *HybridStore) bm25Score(doc Doc, queryTerms map[string]bool) float64 {
	n := len(s.docs)
	docTerms := make(map[string]int)
	for _, t := range strings.Fields(strings.ToLower(doc.Content + " " + doc.Title)) {
		docTerms[t]++
	}
	docLen := len(strings.Fields(doc.Content))

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

// ----- Vector Search (Qdrant REST API) -----

type qdrantPoint struct {
	ID      uint64          `json:"id"`
	Vector  []float64       `json:"vector"`
	Payload json.RawMessage `json:"payload"`
}

type qdrantSearchResult struct {
	Result []struct {
		ID      uint64          `json:"id"`
		Score   float64         `json:"score"`
		Payload json.RawMessage `json:"payload"`
	} `json:"result"`
}

func (s *HybridStore) initCollection(ctx context.Context) error {
	collURL := fmt.Sprintf("%s/collections/%s", s.qdrantURL, s.collection)

	req, _ := http.NewRequestWithContext(ctx, "GET", collURL, nil)
	resp, err := s.httpClient.Do(req)
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		return nil
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
		for j, v := range embeddings[i] {
			vec[j] = v
		}
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

func (s *HybridStore) vectorSearch(vec []float64, limit int, existing []ScoredDoc) ([]ScoredDoc, error) {
	searchURL := fmt.Sprintf("%s/collections/%s/points/search", s.qdrantURL, s.collection)

	qvec := make([]float64, len(vec))
	for i, v := range vec {
		qvec[i] = v
	}

	type searchBody struct {
		Vector      []float64 `json:"vector"`
		Limit       int       `json:"limit"`
		WithPayload bool      `json:"with_payload"`
	}
	body := searchBody{Vector: qvec, Limit: limit, WithPayload: true}
	data, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(context.Background(), "POST", searchURL, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qdrant search: status %d", resp.StatusCode)
	}

	var result qdrantSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var scored []ScoredDoc
	existingSet := make(map[string]bool)
	for _, sd := range existing {
		existingSet[sd.Doc.ID] = true
	}

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
		if existingSet[payload.DocID] {
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

// ----- InitKnowledgeBase -----

// InitKnowledgeBase creates the Qdrant collection and upserts all documents.
// If Qdrant is unreachable, it logs a warning and the store falls back to BM25-only.
func InitKnowledgeBase(ctx context.Context, store *HybridStore) error {
	vecs := make([][]float64, len(store.docs))
	for i, d := range store.docs {
		text := d.Title + "\n" + d.Content
		vec, err := store.embedder.Embed(ctx, text)
		if err != nil {
			return fmt.Errorf("embed doc %s: %w", d.ID, err)
		}
		vecs[i] = vec
	}
	if err := store.initCollection(ctx); err != nil {
		return fmt.Errorf("init qdrant: %w", err)
	}
	if err := store.upsertDocs(ctx, store.docs, vecs); err != nil {
		return fmt.Errorf("upsert docs: %w", err)
	}
	return nil
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
