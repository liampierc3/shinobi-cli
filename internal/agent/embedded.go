package agent

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed agents/*.md
var embeddedAgents embed.FS

func loadEmbeddedAgents() ([]Agent, error) {
	entries, err := fs.ReadDir(embeddedAgents, "agents")
	if err != nil {
		return nil, err
	}
	var result []Agent
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := embeddedAgents.ReadFile("agents/" + entry.Name())
		if err != nil {
			continue
		}
		meta, body := splitFrontMatter(string(data))
		if meta == "" {
			continue
		}
		agent, err := parseMetadata(meta)
		if err != nil {
			continue
		}
		agent.Prompt = strings.TrimSpace(body)
		agent.FilePath = "embedded:" + entry.Name()
		applyAgentFallbacks(&agent, body)
		if err := agent.Validate(); err != nil {
			continue
		}
		result = append(result, agent)
	}
	return result, nil
}
