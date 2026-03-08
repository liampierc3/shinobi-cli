package search

import (
	"context"
)

// Result represents a single web search hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Client defines the behavior required by the UI layer.
type Client interface {
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}
