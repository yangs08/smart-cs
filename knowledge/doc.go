package knowledge

import "context"

// Doc is a knowledge base document.
type Doc struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// DocLoader loads documents for populating the knowledge base.
type DocLoader interface {
	Load(ctx context.Context) ([]Doc, error)
}

// StaticDocLoader returns a fixed set of docs (for testing / demo).
type StaticDocLoader struct {
	docs []Doc
}

func NewStaticDocLoader(docs []Doc) *StaticDocLoader {
	return &StaticDocLoader{docs: docs}
}

func (l *StaticDocLoader) Load(_ context.Context) ([]Doc, error) {
	return l.docs, nil
}
