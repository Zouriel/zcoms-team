package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zouriel/zcoms-team/internal/store"
)

// detectStatus infers a task status from a free-form standup response, plus a
// blocker note when blocked. Mapping (spec): done→Done, blocked→Blocked,
// review→Review, else In Progress; empty → unknown (no change).
func detectStatus(response string) (status, blocker string) {
	r := strings.TrimSpace(response)
	if r == "" {
		return "", ""
	}
	l := strings.ToLower(r)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(l, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("blocked", "waiting", "stuck", "can't", "cannot", "blocker", "need help", "depends on"):
		return store.StatusBlocked, r
	// Work-remaining markers keep it In Progress even if part is "complete".
	case has("remaining", "still ", "left to", "left.", "in progress", "wip", "ongoing", "almost", "tomorrow", "todo"):
		return store.StatusAssigned, ""
	case has("done", "complete", "completed", "finished", "merged", "shipped", "ready for review"):
		return store.StatusDone, ""
	case has("in review", "reviewing", "review", "pr open", "pull request"):
		return store.StatusReview, ""
	default:
		return store.StatusAssigned, "" // in progress
	}
}

func statusLabel(s string) string {
	switch s {
	case store.StatusDone:
		return "Done"
	case store.StatusBlocked:
		return "Blocked"
	case store.StatusReview:
		return "Review"
	case store.StatusAssigned:
		return "In Progress"
	default:
		return "No update"
	}
}

// generateReport formats the group standup summary. staffName maps a staff id to
// a display handle.
func generateReport(updates []*store.TaskUpdate, staffName map[string]string) string {
	if len(updates) == 0 {
		return "Daily Standup Summary\n\n(No updates collected.)"
	}
	// Group by staff, preserving first-seen order.
	var order []string
	byStaff := map[string][]*store.TaskUpdate{}
	for _, u := range updates {
		if _, ok := byStaff[u.StaffMemberID]; !ok {
			order = append(order, u.StaffMemberID)
		}
		byStaff[u.StaffMemberID] = append(byStaff[u.StaffMemberID], u)
	}
	var b strings.Builder
	b.WriteString("Daily Standup Summary\n")
	for _, sid := range order {
		name := staffName[sid]
		if name == "" {
			name = sid
		}
		fmt.Fprintf(&b, "\n%s\n", name)
		for _, u := range byStaff[sid] {
			fmt.Fprintf(&b, "  %s\n  %s\n", u.TaskTitle, statusLabel(u.DetectedStatus))
			if r := strings.TrimSpace(u.StaffResponse); r != "" {
				fmt.Fprintf(&b, "  %s\n", r)
			}
		}
	}
	// Blocked roll-up.
	var blocked []*store.TaskUpdate
	for _, u := range updates {
		if u.DetectedStatus == store.StatusBlocked {
			blocked = append(blocked, u)
		}
	}
	if len(blocked) > 0 {
		b.WriteString("\nBlocked Tasks\n")
		for _, u := range blocked {
			blk := u.Blocker
			if blk == "" {
				blk = u.StaffResponse
			}
			fmt.Fprintf(&b, "  %s\n  %s\n", u.TaskTitle, strings.TrimSpace(blk))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// nextRun returns the next time the schedule "HH:MM" fires in the given timezone,
// strictly after `from`. Falls back to UTC if the zone can't be loaded.
func nextRun(hhmm, tzName string, from time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	var hh, mm int
	if _, e := fmt.Sscanf(strings.TrimSpace(hhmm), "%d:%d", &hh, &mm); e != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return time.Time{}, fmt.Errorf("bad schedule time %q (want HH:MM)", hhmm)
	}
	f := from.In(loc)
	run := time.Date(f.Year(), f.Month(), f.Day(), hh, mm, 0, 0, loc)
	if !run.After(f) {
		run = run.Add(24 * time.Hour)
	}
	return run, nil
}
