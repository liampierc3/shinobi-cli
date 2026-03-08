package search

import (
	"context"
	"errors"
)

type DisabledClient struct{}

func (DisabledClient) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	return nil, errors.New("web search is not configured; set brave_api_key, tavily_api_key, or serpapi_key in ~/.shinobi/config.yaml")
}
