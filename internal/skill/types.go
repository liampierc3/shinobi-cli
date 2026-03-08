package skill

// Skill definition loaded from SKILL.md frontmatter.
type Skill struct {
	Name        string
	Description string
	Prompt      string
	FilePath    string
	Auto        bool
}
