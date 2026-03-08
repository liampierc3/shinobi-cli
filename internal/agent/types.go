package agent

// Agent definition loaded from markdown frontmatter.
type Agent struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
	Model       string   `yaml:"model"`
	Color       string   `yaml:"color"`
	Prompt      string
	FilePath    string
}
