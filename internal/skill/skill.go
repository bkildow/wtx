// Package skill embeds the wtx agent skill printed by `wtx skill`.
package skill

import _ "embed"

// Content is the full agent skill (SKILL.md with frontmatter). The thin
// installable wrapper in skills/wtx/SKILL.md tells agents to run `wtx skill`,
// so this text always matches the installed binary.
//
//go:embed SKILL.md
var Content string
