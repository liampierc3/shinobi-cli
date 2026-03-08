package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const tavilyEndpoint = "https://api.tavily.com/search"

type TavilyClient struct {
	apiKey     string
	httpClient *http.Client
}

func NewTavilyClient(apiKey string) (*TavilyClient, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("missing Tavily API key")
	}
	return &TavilyClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *TavilyClient) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query required")
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}

	body, err := json.Marshal(map[string]any{
		"api_key":              c.apiKey,
		"query":                query,
		"max_results":          limit,
		"include_answer":       false,
		"include_raw_content":  false,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily search failed: %s", resp.Status)
	}

	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(payload.Results))
	for _, item := range payload.Results {
		results = append(results, Result{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Content,
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}
