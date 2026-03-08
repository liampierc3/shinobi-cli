package search

// NewFromConfig builds a search client from the provided keys.
// Priority: Brave → Tavily → SerpAPI → DuckDuckGo (no key) → disabled.
func NewFromConfig(braveKey, tavilyKey, serpapiKey string, ddgEnabled bool) Client {
	if braveKey != "" {
		if c, err := NewBraveClient(braveKey); err == nil {
			return c
		}
	}
	if tavilyKey != "" {
		if c, err := NewTavilyClient(tavilyKey); err == nil {
			return c
		}
	}
	if serpapiKey != "" {
		if c, err := NewSerpAPIClient(serpapiKey); err == nil {
			return c
		}
	}
	if ddgEnabled {
		return NewDuckDuckGoClient()
	}
	return DisabledClient{}
}
