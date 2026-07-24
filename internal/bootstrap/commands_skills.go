package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

func handleSkills(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.skills == nil {
		return CommandResult{State: ResultInfo, Title: "Skills are not configured."}, nil
	}
	if len(req.Args) > 0 {
		return usageHelp(mustEntry("skills"), fmt.Sprintf("Unknown skills subcommand %q.", req.Args[0])), nil
	}
	summaries, err := s.skills.List(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	if len(summaries) == 0 {
		return CommandResult{
			State: ResultInfo,
			Title: "No skills installed.",
			Next:  []string{"/skills add <name>"},
		}, nil
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
	rows := make([][]string, 0, len(summaries))
	for _, summary := range summaries {
		status := "enabled"
		if summary.Disabled {
			status = "disabled"
		}
		rows = append(rows, []string{summary.Name, summary.Description, status})
	}
	return CommandResult{
		TableHeaders: []string{"Skill", "Description", "Status"},
		TableRows:    rows,
		Next:         []string{"/skills show <name>", "/skills add <name>", "/skills remove <name>", "/skills disable <name>", "/skills enable <name>"},
	}, nil
}

func handleSkillsShow(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.skills == nil {
		return CommandResult{State: ResultInfo, Title: "Skills are not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("skills show"), "Expected exactly one <name>."), nil
	}
	skill, err := s.skills.Show(ctx, req.Args[0])
	if err != nil {
		return errorResult(err), nil
	}
	return CommandResult{
		Title:  skill.Name,
		Detail: skill.Description + "\n\n" + skill.Body,
	}, nil
}

// parseSkillProposal splits "<name><space-or-newline><description>\n<body>"
// out of a command's trailing free text: the first whitespace run separates
// the name, then the first line of what remains is the description and
// everything after that is the body — the same subject/body split a commit
// message uses, so it works unchanged whether the owner separates name and
// description with a space (one-line Telegram messages) or a newline
// (a longer pasted skill).
func parseSkillProposal(tail string) (name, description, body string, ok bool) {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return "", "", "", false
	}
	end := strings.IndexFunc(tail, unicode.IsSpace)
	if end < 0 {
		return "", "", "", false
	}
	name = tail[:end]
	rest := strings.TrimSpace(tail[end:])
	description, body, hasBody := strings.Cut(rest, "\n")
	description = strings.TrimSpace(description)
	body = strings.TrimSpace(body)
	if description == "" || !hasBody || body == "" {
		return "", "", "", false
	}
	return name, description, body, true
}

func handleSkillsAdd(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	return handleSkillsWrite(ctx, s, req, "skills add")
}

func handleSkillsEdit(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	return handleSkillsWrite(ctx, s, req, "skills edit")
}

// handleSkillsWrite backs both "skills add" and "skills edit": the
// underlying proposal is the same create-or-replace request either way
// (SkillsService.RequestWrite reports which one it is in the approval
// summary), so the split is purely for the owner's own framing of intent.
func handleSkillsWrite(ctx context.Context, s *CommandService, req CommandRequest, catalogPath string) (CommandResult, error) {
	if s.skills == nil {
		return CommandResult{State: ResultInfo, Title: "Skills are not configured."}, nil
	}
	name, description, body, ok := parseSkillProposal(req.Tail)
	if !ok {
		return usageHelp(mustEntry(catalogPath), "Expected <name>, then a description line, then the skill content on following lines."), nil
	}
	approval, err := s.skills.RequestWrite(ctx, name, description, body)
	if err != nil {
		return errorResult(err), nil
	}
	if s.channel != nil {
		if err := s.channel.DeliverApproval(ctx, s.owner, approval); err != nil {
			return CommandResult{}, err
		}
	}
	return CommandResult{
		State:  ResultInfo,
		Title:  "Proposal for skill " + name + " staged, awaiting approval.",
		Detail: "The owner will see an Approve/Reject prompt.",
		Next:   []string{"/skills"},
	}, nil
}

func handleSkillsRemove(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.skills == nil {
		return CommandResult{State: ResultInfo, Title: "Skills are not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("skills remove"), "Expected exactly one <name>."), nil
	}
	approval, err := s.skills.RequestDelete(ctx, req.Args[0])
	if err != nil {
		return errorResult(err), nil
	}
	if s.channel != nil {
		if err := s.channel.DeliverApproval(ctx, s.owner, approval); err != nil {
			return CommandResult{}, err
		}
	}
	return CommandResult{
		State:  ResultInfo,
		Title:  "Removal of skill " + req.Args[0] + " staged, awaiting approval.",
		Detail: "The owner will see an Approve/Reject prompt.",
		Next:   []string{"/skills"},
	}, nil
}

func handleSkillsDisable(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.skills == nil {
		return CommandResult{State: ResultInfo, Title: "Skills are not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("skills disable"), "Expected exactly one <name>."), nil
	}
	if err := s.skills.Disable(ctx, req.Args[0]); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Title: "Disabled " + req.Args[0] + ".", Detail: "The file is unchanged; re-enable anytime.", Next: []string{"/skills enable " + req.Args[0]}}, nil
}

func handleSkillsEnable(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if s.skills == nil {
		return CommandResult{State: ResultInfo, Title: "Skills are not configured."}, nil
	}
	if len(req.Args) != 1 {
		return usageHelp(mustEntry("skills enable"), "Expected exactly one <name>."), nil
	}
	if err := s.skills.Enable(ctx, req.Args[0]); err != nil {
		return errorResult(err), nil
	}
	return CommandResult{Title: "Enabled " + req.Args[0] + ".", Next: []string{"/skills"}}, nil
}
