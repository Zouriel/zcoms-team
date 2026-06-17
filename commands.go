package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Zouriel/zcoms-team/internal/store"
)

// handleCommand parses and dispatches a `zc team …` command line. Phase 1 covers
// delegator / standup / staff CRUD. (Conversational Telegram flows and task
// workflows arrive in later phases.)
func handleCommand(s *store.Store, actor, text string) (string, error) {
	f := strings.Fields(strings.TrimSpace(text))
	if len(f) == 0 {
		return helpText(), nil
	}
	switch f[0] {
	case "help", "":
		return helpText(), nil
	case "delegator":
		return handleDelegator(s, actor, f[1:])
	case "standup":
		return handleStandup(s, actor, f[1:])
	case "staff":
		return handleStaff(s, actor, f[1:])
	default:
		return "", fmt.Errorf("unknown command %q (try: help)", f[0])
	}
}

func handleDelegator(s *store.Store, actor string, a []string) (string, error) {
	if len(a) == 0 {
		return "", fmt.Errorf("usage: delegator <create|list|delete>")
	}
	switch a[0] {
	case "create":
		// delegator create <name> <github_owner> <project_number>
		if len(a) != 4 {
			return "", fmt.Errorf("usage: delegator create <name> <github_owner> <project_number>")
		}
		num, err := strconv.Atoi(a[3])
		if err != nil {
			return "", fmt.Errorf("project number must be an integer")
		}
		d, err := s.CreateDelegator(a[1], a[2], num, actor)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ Delegator %q created (GitHub %s/#%d).", d.Name, d.GithubOwner, d.GithubProjectNumber), nil
	case "list":
		ds, err := s.ListDelegators()
		if err != nil {
			return "", err
		}
		if len(ds) == 0 {
			return "No delegators yet. Create one: delegator create <name> <owner> <project#>", nil
		}
		var b strings.Builder
		b.WriteString("Delegators:\n")
		for _, d := range ds {
			fmt.Fprintf(&b, "  %s → %s/#%d\n", d.Name, d.GithubOwner, d.GithubProjectNumber)
		}
		return b.String(), nil
	case "delete":
		if len(a) != 2 {
			return "", fmt.Errorf("usage: delegator delete <name>")
		}
		if err := s.DeleteDelegator(a[1], actor); err != nil {
			return "", err
		}
		return fmt.Sprintf("🗑️ Delegator %q deleted.", a[1]), nil
	default:
		return "", fmt.Errorf("usage: delegator <create|list|delete>")
	}
}

func handleStandup(s *store.Store, actor string, a []string) (string, error) {
	if len(a) == 0 {
		return "", fmt.Errorf("usage: standup <create|list|delete>")
	}
	switch a[0] {
	case "create":
		// standup create <name> <delegator> <telegram_group> <time> <timezone>
		if len(a) != 6 {
			return "", fmt.Errorf("usage: standup create <name> <delegator> <telegram_group> <HH:MM> <timezone>")
		}
		del, err := s.DelegatorByName(a[2])
		if err != nil {
			return "", fmt.Errorf("no delegator named %q", a[2])
		}
		su, err := s.CreateStandup(a[1], del.ID, a[3], a[4], a[5], actor)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ Standup %q created → %s at %s (%s), reports to %s.", su.Name, del.Name, su.ScheduleTime, su.Timezone, su.TelegramGroup), nil
	case "list":
		var delID string
		if len(a) == 2 {
			del, err := s.DelegatorByName(a[1])
			if err != nil {
				return "", fmt.Errorf("no delegator named %q", a[1])
			}
			delID = del.ID
		}
		sus, err := s.ListStandups(delID)
		if err != nil {
			return "", err
		}
		if len(sus) == 0 {
			return "No standups configured.", nil
		}
		var b strings.Builder
		b.WriteString("Standups:\n")
		for _, su := range sus {
			fmt.Fprintf(&b, "  %s → %s %s (%s)\n", su.Name, su.TelegramGroup, su.ScheduleTime, su.Timezone)
		}
		return b.String(), nil
	case "delete":
		if len(a) != 2 {
			return "", fmt.Errorf("usage: standup delete <name>")
		}
		if err := s.DeleteStandup(a[1], actor); err != nil {
			return "", err
		}
		return fmt.Sprintf("🗑️ Standup %q deleted.", a[1]), nil
	default:
		return "", fmt.Errorf("usage: standup <create|list|delete>")
	}
}

func handleStaff(s *store.Store, actor string, a []string) (string, error) {
	if len(a) < 2 {
		return "", fmt.Errorf("usage: staff <add|remove|role|limit|list> <delegator> …")
	}
	sub := a[0]
	del, err := s.DelegatorByName(a[1])
	if err != nil {
		return "", fmt.Errorf("no delegator named %q", a[1])
	}
	switch sub {
	case "add":
		// staff add <delegator> <telegram> <github> <role> <limit>
		if len(a) != 6 {
			return "", fmt.Errorf("usage: staff add <delegator> <@telegram> <github> <role> <limit>")
		}
		limit, err := strconv.Atoi(a[5])
		if err != nil || limit < 0 {
			return "", fmt.Errorf("limit must be a non-negative integer")
		}
		st, err := s.AddStaff(del.ID, a[2], a[3], a[4], limit, actor)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ Added %s (%s, role=%s, limit=%d) to %s.", st.TelegramUsername, st.GithubUsername, st.Role, st.MaxActiveTasks, del.Name), nil
	case "remove":
		if len(a) != 3 {
			return "", fmt.Errorf("usage: staff remove <delegator> <@telegram>")
		}
		if err := s.RemoveStaff(del.ID, a[2], actor); err != nil {
			return "", err
		}
		return fmt.Sprintf("🗑️ Removed %s from %s.", a[2], del.Name), nil
	case "role":
		if len(a) != 4 {
			return "", fmt.Errorf("usage: staff role <delegator> <@telegram> <admin|lead|staff>")
		}
		if err := s.SetStaffRole(del.ID, a[2], a[3], actor); err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ %s is now %s in %s.", a[2], strings.ToLower(a[3]), del.Name), nil
	case "limit":
		if len(a) != 4 {
			return "", fmt.Errorf("usage: staff limit <delegator> <@telegram> <n>")
		}
		n, err := strconv.Atoi(a[3])
		if err != nil || n < 0 {
			return "", fmt.Errorf("limit must be a non-negative integer")
		}
		if err := s.SetStaffLimit(del.ID, a[2], n, actor); err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ %s task limit set to %d in %s.", a[2], n, del.Name), nil
	case "list":
		sts, err := s.ListStaff(del.ID)
		if err != nil {
			return "", err
		}
		if len(sts) == 0 {
			return fmt.Sprintf("No staff in %s yet.", del.Name), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Staff in %s:\n", del.Name)
		for _, st := range sts {
			fmt.Fprintf(&b, "  %s (%s) — %s, limit %d\n", st.TelegramUsername, st.GithubUsername, st.Role, st.MaxActiveTasks)
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("usage: staff <add|remove|role|limit|list> <delegator> …")
	}
}

func helpText() string {
	return strings.Join([]string{
		"zc-team commands:",
		"  delegator create <name> <github_owner> <project#>",
		"  delegator list | delegator delete <name>",
		"  standup create <name> <delegator> <@group> <HH:MM> <timezone>",
		"  standup list [delegator] | standup delete <name>",
		"  staff add <delegator> <@telegram> <github> <role> <limit>",
		"  staff remove|role|limit|list <delegator> …",
	}, "\n")
}
