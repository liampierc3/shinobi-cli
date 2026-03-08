package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const serpapiEndpoint = "https://serpapi.com/search"

type SerpAPIClient struct {
	apiKey     string
	httpClient *http.Client
}

func NewSerpAPIClient(apiKey string) (*SerpAPIClient, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("missing SerpAPI key")
	}
	return &SerpAPIClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *SerpAPIClient) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query required")
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}

	endpoint, err := url.Parse(serpapiEndpoint)
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("q", query)
	q.Set("api_key", c.apiKey)
	q.Set("engine", "google")
	q.Set("num", fmt.Sprintf("%d", limit))
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("serpapi search failed: %s", resp.Status)
	}

	var payload struct {
		OrganicResults []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic_results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(payload.OrganicResults))
	for _, item := range payload.OrganicResults {
		results = append(results, Result{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Snippet,
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}
