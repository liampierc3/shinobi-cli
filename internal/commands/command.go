package commands

import "fmt"

// Command represents a slash command loaded from markdown.
type Command struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Color       string `yaml:"color"`
	Model       string `yaml:"model"`
	Priority    int    `yaml:"priority"` // Lower = shows first (0 = top)
	// HideDescription suppresses rendering the description in the UI.
	HideDescription bool `yaml:"hide_description"`
	Prompt          string
	FilePath        string
}

// Validate ensures required fields are present.
func (c Command) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("command missing name")
	}
	if c.Description == "" {
		return fmt.Errorf("command %q missing description", c.Name)
	}
	if c.Prompt == "" {
		return fmt.Errorf("command %q missing prompt body", c.Name)
	}
	return nil
}
