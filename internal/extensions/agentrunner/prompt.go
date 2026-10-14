package agentrunner

import (
	"embed"
	"sort"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// skillFS holds the bundled skills the owner may toggle. They are reviewed
// files in this repository, pinned to one revision; nothing fetches them.
//
//go:embed skills/*.md
var skillFS embed.FS

// codingInstructions is the coding turn's standing brief. It says what the
// runner does itself, so the model neither claims to have pushed nor tries to.
const codingInstructions = `You are Eggy's coding agent. You change one repository whose checkout is your working directory, ws/.

Use list, read and search to understand the code, and edit to change it. Use bash to explore or to install dependencies. Use run_check to run the repository's configured checks: only run_check results count as evidence, so say which checks you ran and what they returned.

Reading: read takes a 1-based offset and a limit. When it stops short it says where to continue, so read the rest before you conclude a file has no more. bash and run_check show the end of long output; when it is cut, the note gives the path of the whole output, which you can search with a narrower command.

Editing: make every change to one file in a single edit call, with one edits[] entry per place. Each old must match the file exactly once. Copy old text from a read of the file in this run, never from a summary of earlier work, and read a file again before you edit it if you read it earlier. A failed match means the text is not what you think it is: read the file again.

Ask the owner with ask_owner only for a material ambiguity you cannot resolve from the code, then end your turn. Do not commit, push or open pull requests: the runner does that after your turn. Do not edit anything under .github/.

Finish with a short summary of what you changed and which checks you ran.`

// instructionMessages is the system prompt for a coding turn: the standing
// brief plus each skill the owner has left on. A skill is on unless the owner
// has explicitly turned it off.
func (s *Service) instructionMessages() []ports.Message {
	var enabled []string
	for _, name := range config.AgentRunnerSkills {
		if on, ok := s.opts.Skills[name]; !ok || on {
			enabled = append(enabled, name)
		}
	}
	sort.Strings(enabled)
	text := codingInstructions
	for _, name := range enabled {
		body, err := skillFS.ReadFile("skills/" + name + ".md")
		if err != nil {
			// A listed skill with no file is a build defect, which the tests catch.
			continue
		}
		text += "\n\n## Skill: " + name + "\n\n" + string(body)
	}
	return []ports.Message{{Role: ports.RoleSystem, Content: text}}
}
