package lmstudio

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/liampierc3/shinobi-cli/internal/llm"
)

func TestChatFallsBackToReasoningContent(t *testing.T) {
	t.Parallel()

	client := NewClient("http://example.test/v1", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/chat/completions" {
				return response(http.StatusNotFound, `{"error":"not found"}`), nil
			}
			return response(http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"","reasoning_content":"ACTION: RESPOND"}}]}`), nil
		}),
	}

	got, err := client.Chat(context.Background(), llm.ChatRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got != "ACTION: RESPOND" {
		t.Fatalf("unexpected chat response: %q", got)
	}
}

func TestChatStreamReadsReasoningContentChunks(t *testing.T) {
	t.Parallel()

	client := NewClient("http://example.test/v1", "")
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/chat/completions" {
				return response(http.StatusNotFound, `{"error":"not found"}`), nil
			}
			sse := strings.Join([]string{
				`data: {"choices":[{"delta":{"reasoning_content":"hello"}}]}`,
				``,
				`data: {"choices":[{"delta":{"reasoning_content":" world"}}]}`,
				``,
				`data: {"choices":[{"delta":{"reasoning_content":"\n"}}]}`,
				``,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				``,
			}, "\n")
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(sse)),
			}, nil
		}),
	}

	ch := make(chan llm.StreamToken, 16)
	err := client.ChatStream(context.Background(), llm.ChatRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: "user", Content: "hi"},
		},
	}, ch)
	if err != nil {
		t.Fatalf("ChatStream returned error: %v", err)
	}

	var tokens []llm.StreamToken
	for {
		token := <-ch
		tokens = append(tokens, token)
		if token.Done {
			break
		}
	}

	if len(tokens) != 4 {
		t.Fatalf("expected 4 tokens (3 reasoning + done), got %d", len(tokens))
	}
	if !tokens[0].Thinking || tokens[0].Content != "hello" {
		t.Fatalf("unexpected first token: %#v", tokens[0])
	}
	if !tokens[1].Thinking || tokens[1].Content != " world" {
		t.Fatalf("unexpected second token: %#v", tokens[1])
	}
	if !tokens[2].Thinking || tokens[2].Content != "\n" {
		t.Fatalf("unexpected third token: %#v", tokens[2])
	}
	if !tokens[3].Done {
		t.Fatalf("expected final done token, got %#v", tokens[3])
	}
}

func TestDecodeChunkTextPreservesWhitespace(t *testing.T) {
	t.Parallel()

	raw := []byte(`" world"`)
	got := decodeChunkText(raw)
	if got != " world" {
		t.Fatalf("expected preserved leading space, got %q", got)
	}
}

func TestDecodeChunkTextFromStructuredArray(t *testing.T) {
	t.Parallel()

	raw := []byte(`[{"type":"output_text","text":"A "},{"type":"output_text","text":"B"}]`)
	got := decodeChunkText(raw)
	if got != "A B" {
		t.Fatalf("expected whitespace-preserving concatenation, got %q", got)
	}
}

func TestDecodeChunkTextFromStructuredArrayWithNewline(t *testing.T) {
	t.Parallel()

	raw := []byte(`[{"type":"output_text","text":"line1"},{"type":"output_text","text":"\n"},{"type":"output_text","text":"line2"}]`)
	got := decodeChunkText(raw)
	if got != "line1\nline2" {
		t.Fatalf("expected newline-preserving concatenation, got %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(statusCode int, body string) *http.Response {
	status := http.StatusText(statusCode)
	if status == "" {
		status = "Unknown"
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     strings.TrimSpace(strings.Join([]string{strconv.Itoa(statusCode), status}, " ")),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
