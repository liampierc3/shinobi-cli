package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config captures user-level preferences.
type Config struct {
	BraveAPIKey      string   `yaml:"brave_api_key"`
	BraveToken       string   `yaml:"brave_subscription_token"` // legacy alias
	TavilyAPIKey     string   `yaml:"tavily_api_key"`
	SerpAPIKey       string   `yaml:"serpapi_key"`
	DDGEnabled       bool     `yaml:"duckduckgo_enabled"`
	FilesystemRoot  string   `yaml:"filesystem_root"`
	FilesystemRoots []string `yaml:"filesystem_roots"`
	DefaultModel     string   `yaml:"default_model"`
	DefaultAgent     string   `yaml:"default_agent"`
	ContextPaths     []string `yaml:"context_paths"`
	AutoLoadSkills   []string `yaml:"auto_load_skills"`

	// Named backend URLs. OllamaURL and LMStudioURL are the preferred fields.
	// BackendURL is the legacy single-backend field kept for backward compat —
	// it is treated as the LM Studio URL when LMStudioURL is empty.
	OllamaURL     string `yaml:"ollama_url"`
	LMStudioURL   string `yaml:"lm_studio_url"`
	BackendURL    string `yaml:"backend_url"`     // legacy fallback for LM Studio
	BackendAPIKey string `yaml:"backend_api_key"` // used by LM Studio client

	// ActiveBackend selects which configured backend to use: "ollama" or "lmstudio".
	// When empty and both URLs are set, the startup picker prompts the user.
	ActiveBackend string `yaml:"active_backend"`

	// Additional agent directories to scan beyond the defaults
	AgentDirs []string `yaml:"agent_dirs"`

	// Skills directory — root folder containing skill subfolders with SKILL.md files.
	// Defaults to ~/memory/ai/skills if unset.
	SkillsDir string `yaml:"skills_dir"`
}

// Save writes cfg to ~/.shinobi/config.yaml, creating the directory if needed.
func Save(cfg Config) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".shinobi")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Load attempts to read ~/.shinobi/config.yaml. Missing file is not an error.
func Load() (Config, error) {
	var cfg Config
	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, err
	}
	// Primary config location for Shinobi.
	path := filepath.Join(home, ".shinobi", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		} else {
			return cfg, fmt.Errorf("read config: %w", err)
		}
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	cfg = expandPaths(cfg, home)
	return cfg, nil
}

// expandPaths replaces leading ~ with the home directory in all path fields.
func expandPaths(cfg Config, home string) Config {
	expand := func(p string) string {
		if p == "~" {
			return home
		}
		if len(p) >= 2 && p[:2] == "~/" {
			return filepath.Join(home, p[2:])
		}
		return p
	}
	cfg.FilesystemRoot = expand(cfg.FilesystemRoot)
	cfg.SkillsDir = expand(cfg.SkillsDir)
	for i, p := range cfg.FilesystemRoots {
		cfg.FilesystemRoots[i] = expand(p)
	}
	// backend URLs are not filesystem paths, no expansion needed
	for i, p := range cfg.AgentDirs {
		cfg.AgentDirs[i] = expand(p)
	}
	for i, p := range cfg.ContextPaths {
		cfg.ContextPaths[i] = expand(p)
	}
	return cfg
}

// EffectiveLMStudioURL returns LMStudioURL, falling back to BackendURL for
// backward compatibility with configs that predate the named backend fields.
func (c Config) EffectiveLMStudioURL() string {
	if c.LMStudioURL != "" {
		return c.LMStudioURL
	}
	return c.BackendURL
}
