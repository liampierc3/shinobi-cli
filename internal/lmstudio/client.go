package lmstudio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/liampierc3/shinobi-cli/internal/llm"
)

const (
	defaultBaseURL = "http://127.0.0.1:1234/v1"
)

// Client implements llm.Client for an LM Studio OpenAI-compatible server.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient constructs a Client with explicit URL and API key.
// Falls back to env vars and then defaults for any empty values.
func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("SHINOBI_BACKEND_URL"))
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	// Auto-append /v1 if the URL has no path (e.g. http://127.0.0.1:1234)
	if u, err := url.Parse(baseURL); err == nil && (u.Path == "" || u.Path == "/") {
		baseURL += "/v1"
	}

	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("SHINOBI_BACKEND_API_KEY"))
	}

	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: http.DefaultClient,
	}
}

// NewClientFromEnv constructs a Client using SHINOBI_BACKEND_URL and
// SHINOBI_BACKEND_API_KEY environment variables.
func NewClientFromEnv() *Client {
	return NewClient("", "")
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
		} `json:"message"`
	} `json:"choices"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ChatStream sends a streaming chat request to LM Studio and forwards tokens
// to the provided channel, mirroring OpenAI-style streaming responses.
func (c *Client) ChatStream(ctx context.Context, req llm.ChatRequest, ch chan<- llm.StreamToken) error {
	if c == nil {
		return fmt.Errorf("lmstudio client is nil")
	}
	modelID, err := c.resolveModelID(ctx, req.Model)
	if err != nil {
		return err
	}
	payload := chatRequest{
		Model:    modelID,
		Messages: toChatMessages(req.Messages),
		Stream:   true,
	}

	httpReq, err := c.newRequest(ctx, payload)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("lmstudio: streaming request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("lmstudio: streaming request failed with status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	// Increase the maximum token size to handle larger chunks safely.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			select {
			case ch <- llm.StreamToken{Content: "", Done: true}:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("lmstudio: decode stream chunk: %w", err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		content := chunk.Choices[0].Delta.Content
		if content != "" {
			select {
			case ch <- llm.StreamToken{Content: content, Done: false}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if strings.TrimSpace(content) == "" {
			reasoning := chunk.Choices[0].Delta.Reasoning
			if reasoning != "" {
				select {
				case ch <- llm.StreamToken{Content: reasoning, Thinking: true, Done: false}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if chunk.Choices[0].FinishReason != "" {
			select {
			case ch <- llm.StreamToken{Content: "", Done: true}:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("lmstudio: stream read error: %w", err)
	}

	// If we exit the loop without an explicit done marker, signal completion.
	select {
	case ch <- llm.StreamToken{Content: "", Done: true}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// Chat sends a non-streaming chat request to LM Studio and returns the full
// assistant response content.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (string, error) {
	if c == nil {
		return "", fmt.Errorf("lmstudio client is nil")
	}
	modelID, err := c.resolveModelID(ctx, req.Model)
	if err != nil {
		return "", err
	}

	payload := chatRequest{
		Model:    modelID,
		Messages: toChatMessages(req.Messages),
		Stream:   false,
	}

	httpReq, err := c.newRequest(ctx, payload)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("lmstudio: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("lmstudio: request failed with status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("lmstudio: read response: %w", err)
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("lmstudio: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("lmstudio: empty choices in response")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content != "" {
		return content, nil
	}
	// ToolRunner uses non-streaming Chat(); some models place output in
	// message.reasoning when message.content is empty.
	return strings.TrimSpace(parsed.Choices[0].Message.Reasoning), nil
}

// ListModels returns the LM Studio model IDs that Shinobi is configured to use.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	if c == nil {
		return nil, fmt.Errorf("lmstudio client is nil")
	}
	url := c.baseURL + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: create models request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: models request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("lmstudio: models request failed with status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: read models response: %w", err)
	}
	var parsed modelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("lmstudio: decode models response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("lmstudio: empty models response")
	}
	out := make([]string, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		if strings.TrimSpace(item.ID) != "" {
			out = append(out, strings.TrimSpace(item.ID))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("lmstudio: empty model ids in response")
	}
	return out, nil
}

func (c *Client) resolveModelID(ctx context.Context, requested string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("lmstudio client is nil")
	}
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested, nil
	}
	if ids, err := c.ListModels(ctx); err == nil && len(ids) > 0 {
		return ids[0], nil
	} else if err != nil {
		return "", err
	}
	return "", fmt.Errorf("lmstudio: no model id available")
}

// newRequest builds an HTTP request for the LM Studio chat completions endpoint.
func (c *Client) newRequest(ctx context.Context, payload chatRequest) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("lmstudio: encode request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("lmstudio: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}

// toChatMessages converts generic llm messages into LM Studio chat messages.
// Consecutive messages with the same role are merged to satisfy models (e.g.
// Qwen3.5) whose Jinja templates require strictly alternating roles.
func toChatMessages(msgs []llm.Message) []chatMessage {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]chatMessage, 0, len(msgs))
	for _, msg := range msgs {
		if len(out) > 0 && out[len(out)-1].Role == msg.Role {
			out[len(out)-1].Content += "\n\n" + msg.Content
		} else {
			out = append(out, chatMessage{Role: msg.Role, Content: msg.Content})
		}
	}
	return out
}
