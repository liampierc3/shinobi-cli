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

const (
	braveDefaultEndpoint = "https://api.search.brave.com/res/v1/web/search"
	braveHeaderToken     = "x-subscription-token"
)

// BraveClient implements Client using Brave Search API.
type BraveClient struct {
	subscriptionToken string
	endpoint          string
	httpClient        *http.Client
}

// NewBraveClient creates a Brave-backed search client. Returns an error if token is empty.
func NewBraveClient(subscriptionToken string) (*BraveClient, error) {
	subscriptionToken = strings.TrimSpace(subscriptionToken)
	if subscriptionToken == "" {
		return nil, errors.New("missing Brave subscription token")
	}
	return &BraveClient{
		subscriptionToken: subscriptionToken,
		endpoint:          braveDefaultEndpoint,
		httpClient:        &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *BraveClient) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if c == nil {
		return nil, errors.New("search client nil")
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query required")
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", limit))
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(braveHeaderToken, c.subscriptionToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave search failed: %s", resp.Status)
	}

	var payload braveResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(payload.Web.Results))
	for _, item := range payload.Web.Results {
		results = append(results, Result{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Description,
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}
