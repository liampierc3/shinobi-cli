package config

// Backend identifies which underlying service a model is hosted on.
type Backend string

const (
	BackendLMStudio Backend = "lmstudio"
	BackendOllama   Backend = "ollama"
)

// ModelRoute describes how a model identifier maps to a backend.
type ModelRoute struct {
	Label        string   // Optional display label
	Backend      Backend  // e.g. "lmstudio"
	ID           string   // Backend-specific model ID
	Capabilities []string // e.g. ["chat","code","reasoning"]
	Default      bool     // Reserved for future use
}
