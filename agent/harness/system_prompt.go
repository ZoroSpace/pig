// pi: src/harness/system-prompt.ts — model-visible skills system-prompt
// block.
package harness

import "strings"

// FormatSkillsForSystemPrompt renders the model-visible skills block for the
// system prompt (pi: formatSkillsForSystemPrompt). Skills with
// DisableModelInvocation are excluded; an empty result means "no skills
// section".
func FormatSkillsForSystemPrompt(skills []Skill) string {
	var visible []Skill
	for _, skill := range skills {
		if skill.DisableModelInvocation != nil && *skill.DisableModelInvocation {
			continue
		}
		visible = append(visible, skill)
	}
	if len(visible) == 0 {
		return ""
	}

	lines := []string{
		"The following skills provide specialized instructions for specific tasks.",
		"Read the full skill file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}

	for _, skill := range visible {
		lines = append(lines, "  <skill>")
		lines = append(lines, "    <name>"+escapeXml(skill.Name)+"</name>")
		lines = append(lines, "    <description>"+escapeXml(skill.Description)+"</description>")
		lines = append(lines, "    <location>"+escapeXml(skill.FilePath)+"</location>")
		lines = append(lines, "  </skill>")
	}

	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

// escapeXml escapes the five XML entities in pi's escapeXml order. A
// strings.Replacer applies simultaneously, so ordering cannot double-escape.
func escapeXml(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	).Replace(value)
}
