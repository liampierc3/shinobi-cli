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

// DuckDuckGoClient uses the DDG instant answer API.
// No API key required. Results are limited compared to paid providers.
type DuckDuckGoClient struct {
	httpClient *http.Client
}

func NewDuckDuckGoClient() *DuckDuckGoClient {
	return &DuckDuckGoClient{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *DuckDuckGoClient) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query required")
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}

	endpoint, err := url.Parse("https://api.duckduckgo.com/")
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("no_redirect", "1")
	q.Set("no_html", "1")
	q.Set("skip_disambig", "1")
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "shinobi/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo search failed: %s", resp.Status)
	}

	var payload struct {
		AbstractTitle string `json:"Heading"`
		AbstractText  string `json:"AbstractText"`
		AbstractURL   string `json:"AbstractURL"`
		RelatedTopics []struct {
			Text      string `json:"Text"`
			FirstURL  string `json:"FirstURL"`
			Result    string `json:"Result"`
		} `json:"RelatedTopics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	var results []Result

	// Lead with the abstract if present
	if payload.AbstractText != "" && payload.AbstractURL != "" {
		results = append(results, Result{
			Title:   payload.AbstractTitle,
			URL:     payload.AbstractURL,
			Snippet: payload.AbstractText,
		})
	}

	for _, topic := range payload.RelatedTopics {
		if len(results) >= limit {
			break
		}
		if topic.FirstURL == "" || topic.Text == "" {
			continue
		}
		results = append(results, Result{
			Title:   topic.Text,
			URL:     topic.FirstURL,
			Snippet: topic.Text,
		})
	}

	if len(results) == 0 {
		return nil, errors.New("no results from DuckDuckGo — query may be too specific for the instant answer API")
	}

	return results, nil
}
