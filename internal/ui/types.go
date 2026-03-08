package ui

import "time"

// Message represents a single message in the conversation
type Message struct {
	Role          string    // "user", "assistant", "system", "error"
	Content       string    // The actual message content
	Display       string    // Optional text to render instead of Content
	Thinking      string    // Model's reasoning/thinking process (hidden by default)
	RenderedCache string    // Cached rendered markdown (empty if not rendered)
	Timestamp     time.Time // When the message was created
	Model         string    // Which model generated this (for assistant messages)
}

// VisibleContent returns the text shown in the UI.
func (m Message) VisibleContent() string {
	if m.Display != "" {
		return m.Display
	}
	return m.Content
}

// NewUserMessage creates a new user message
func NewUserMessage(content string) Message {
	return Message{
		Role:      "user",
		Content:   content,
		Display:   content,
		Timestamp: time.Now(),
	}
}

// NewAssistantMessage creates a new assistant message
func NewAssistantMessage(content string, model string) Message {
	return Message{
		Role:      "assistant",
		Content:   content,
		Display:   content,
		Timestamp: time.Now(),
		Model:     model,
	}
}

// NewSystemMessage creates a new system message
func NewSystemMessage(content string) Message {
	return Message{
		Role:      "system",
		Content:   content,
		Display:   content,
		Timestamp: time.Now(),
	}
}

// NewUIOnlySystemMessage creates a system message that is displayed in the UI
// but omitted from model context. Content is prefixed to allow filtering.
func NewUIOnlySystemMessage(display string) Message {
	return Message{
		Role:      "system",
		Content:   "UI_ONLY:" + display,
		Display:   display,
		Timestamp: time.Now(),
	}
}

// NewErrorMessage creates a new error message
func NewErrorMessage(content string) Message {
	return Message{
		Role:      "error",
		Content:   content,
		Display:   content,
		Timestamp: time.Now(),
	}
}
