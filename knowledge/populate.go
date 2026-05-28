package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// BM25Stats holds global corpus statistics needed for runtime BM25 scoring.
type BM25Stats struct {
	AvgDocLen float64          `json:"avg_doc_len"`
	TotalDocs int              `json:"total_docs"`
	NTerms    map[string]int   `json:"n_terms"`
}

func computeBM25Stats(docs []Doc) *BM25Stats {
	totalLen := 0
	nTerms := make(map[string]int)
	for _, d := range docs {
		totalLen += len(tokenize(d.Content + " " + d.Title))
		terms := collectTerms(d.Content + " " + d.Title)
		for t := range terms {
			nTerms[t]++
		}
	}
	return &BM25Stats{
		AvgDocLen: math.Max(float64(totalLen)/float64(len(docs)), 1),
		TotalDocs: len(docs),
		NTerms:    nTerms,
	}
}

func (s *BM25Stats) Save(path string) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal bm25 stats: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// InitKnowledgeBase populates Qdrant from a DocLoader and persists BM25 corpus
// statistics for runtime two-stage retrieval.
func InitKnowledgeBase(ctx context.Context, store *HybridStore, loader DocLoader) error {
	docs, err := loader.Load(ctx)
	if err != nil {
		return fmt.Errorf("load docs: %w", err)
	}
	if len(docs) == 0 {
		return fmt.Errorf("no documents to populate")
	}

	// Compute BM25 stats before embedding (cheap).
	stats := computeBM25Stats(docs)

	// Embed all documents.
	vecs := make([][]float64, len(docs))
	for i, d := range docs {
		text := d.Title + "\n" + d.Content
		vec, err := store.embedder.Embed(ctx, text)
		if err != nil {
			return fmt.Errorf("embed doc %s: %w", d.ID, err)
		}
		vecs[i] = vec
	}

	// Init Qdrant collection + upsert.
	if err := store.initCollection(ctx); err != nil {
		return fmt.Errorf("init qdrant: %w", err)
	}
	if err := store.upsertDocs(ctx, docs, vecs); err != nil {
		return fmt.Errorf("upsert docs: %w", err)
	}

	// Persist BM25 stats for runtime use.
	if store.statsPath != "" {
		if err := stats.Save(store.statsPath); err != nil {
			return fmt.Errorf("save bm25 stats: %w", err)
		}
	}

	// Load stats into the store so the current process can use them immediately.
	store.avgDocLen = stats.AvgDocLen
	store.nTerms = stats.NTerms
	store.totalDocs = stats.TotalDocs

	return nil
}
