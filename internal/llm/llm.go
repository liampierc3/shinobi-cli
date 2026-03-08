package llm

import "context"

// Message represents a single chat message exchanged with a model backend.
type Message struct {
	Role    string
	Content string
}

// ChatRequest represents a chat completion style request to a model backend.
type ChatRequest struct {
	Messages []Message
	Model    string
}

// StreamToken represents a token or text chunk emitted during streaming.
// When Done is true, the stream has completed.
type StreamToken struct {
	Content  string
	Thinking bool
	Done     bool
}

// Client is a generic interface for LLM backends that support chat-style
// completions with optional streaming and model enumeration.
type Client interface {
	// ChatStream sends a streaming chat request. Implementations should write
	// tokens to ch and return when streaming is finished or an error occurs.
	ChatStream(ctx context.Context, req ChatRequest, ch chan<- StreamToken) error

	// Chat sends a non-streaming chat request and returns the full content
	// of the assistant's reply.
	Chat(ctx context.Context, req ChatRequest) (string, error)

	// ListModels returns a list of model identifiers supported by this client.
	ListModels(ctx context.Context) ([]string, error)
}
