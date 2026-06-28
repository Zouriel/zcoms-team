package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Zouriel/zcoms-team/internal/reports"
	"github.com/Zouriel/zcoms-team/internal/store"
	commsclient "github.com/Zouriel/zcoms/client"
)

const reportHour = 8 // weekly/monthly reports go out at ~08:00 (server local)

func reportsDir() string {
	dir, err := commsclient.DefaultAppDir()
	if err != nil {
		return os.TempDir()
	}
	p := filepath.Join(dir, "zc-team", "reports")
	_ = os.MkdirAll(p, 0o700)
	return p
}

// buildData assembles a report for a delegator over [from, to).
func (co *Coordinator) buildData(del *store.Delegator, title, period string, from, to time.Time, standupNote string) reports.Data {
	completed, _ := co.e.s.CompletedBetween(del.ID, from, to)
	active, _ := co.e.s.TasksWhere(del.ID, store.StatusAssigned, store.StatusReview)
	blocked, _ := co.e.s.TasksWhere(del.ID, store.StatusBlocked)
	backlog, _ := co.e.s.AvailableTasks(del.ID, 50)
	staff, _ := co.e.s.ListStaff(del.ID)
	runs, _ := co.e.s.CountRunsBetween(del.ID, from, to)

	name := map[string]string{}
	for _, st := range staff {
		name[st.ID] = st.TelegramUsername
	}
	tally := func(list []*store.Task) map[string]int {
		m := map[string]int{}
		for _, t := range list {
			m[t.AssignedStaff]++
		}
		return m
	}
	cDone, cActive, cBlocked := tally(completed), tally(active), tally(blocked)

	var staffStats []reports.StaffStat
	for _, st := range staff {
		staffStats = append(staffStats, reports.StaffStat{
			Handle: st.TelegramUsername, GithubUser: st.GithubUsername, Role: st.Role,
			Active: cActive[st.ID], Completed: cDone[st.ID], Blocked: cBlocked[st.ID],
		})
	}

	return reports.Data{
		Title:       title,
		Delegator:   del.Name,
		GithubInfo:  fmt.Sprintf("%s/#%d", del.GithubOwner, del.GithubProjectNumber),
		Period:      period,
		GeneratedAt: time.Now(),
		Stats: reports.Stats{
			Completed: len(completed), Active: len(active), Blocked: len(blocked),
			Unassigned: len(backlog), Standups: runs,
		},
		Staff:       staffStats,
		Completed:   toLines(completed, name),
		Active:      toLines(active, name),
		Blocked:     toLines(blocked, name),
		Backlog:     toLines(backlog, name),
		StandupNote: standupNote,
	}
}

func toLines(tasks []*store.Task, name map[string]string) []reports.TaskLine {
	var out []reports.TaskLine
	for _, t := range tasks {
		out = append(out, reports.TaskLine{
			Title: t.Title, Priority: t.Priority, Status: statusLabel(t.Status), Assignee: name[t.AssignedStaff],
		})
	}
	return out
}

// generateAndSend renders a report PDF and sends it to the delegator's creator.
func (co *Coordinator) generateAndSend(del *store.Delegator, d reports.Data, fileTag, caption string) {
	path := filepath.Join(reportsDir(), fmt.Sprintf("%s-%s.pdf", del.Name, fileTag))
	if err := reports.RenderPDF(d, path); err != nil {
		log.Printf("[report] render %s: %v", path, err)
		return
	}
	if co.ipc == nil {
		log.Printf("[report] no daemon IPC; report saved to %s but not sent", path)
		return
	}
	if _, err := co.ipc.SendFile(del.CreatedBy, path, caption); err != nil {
		log.Printf("[report] couldn't send %s to %s: %v", path, del.CreatedBy, err)
	}
}

// sendDailyReport is fired right after a standup completes.
func (co *Coordinator) sendDailyReport(del *store.Delegator, standupNote string) {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	d := co.buildData(del, "Daily Report", now.Format("2006-01-02"), startOfDay, now, standupNote)
	co.generateAndSend(del, d, "daily-"+now.Format("2006-01-02"), "📊 Daily report for "+del.Name)
}

// runPeriodicReports fires weekly (Saturday) and monthly (1st) reports once per
// occurrence, for every delegator, sending each to its creator.
func (co *Coordinator) runPeriodicReports(now time.Time) {
	if now.Weekday() == time.Saturday {
		co.periodic("weekly", now, func(del *store.Delegator) reports.Data {
			from := now.AddDate(0, 0, -7)
			return co.buildData(del, "Weekly Report", fmt.Sprintf("%s – %s", from.Format("Jan 2"), now.Format("Jan 2, 2006")), from, now, "")
		})
	}
	if now.Day() == 1 {
		co.periodic("monthly", now, func(del *store.Delegator) reports.Data {
			to := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
			from := to.AddDate(0, -1, 0)
			return co.buildData(del, "Monthly Report", from.Format("January 2006"), from, to, "")
		})
	}
}

func (co *Coordinator) periodic(period string, now time.Time, build func(*store.Delegator) reports.Data) {
	dels, _ := co.e.s.ListDelegators()
	for _, del := range dels {
		key := period + ":" + del.ID + ":" + now.Format("2006-01-02")
		co.mu.Lock()
		if co.sentReports[key] {
			co.mu.Unlock()
			continue
		}
		co.sentReports[key] = true
		co.mu.Unlock()
		d := build(del)
		co.generateAndSend(del, d, period+"-"+now.Format("2006-01-02"), "📊 "+d.Title+" for "+del.Name)
	}
}
