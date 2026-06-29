package main

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	agentclient "github.com/Zouriel/zcoms-agent/client"
	"github.com/Zouriel/zcoms-agent/scheduler"
	"github.com/Zouriel/zcoms-team/internal/store"
	commsclient "github.com/Zouriel/zcoms/client"
)

// --- the team↔errands standup interview protocol ----------------------------

// The interview *request* types now live in agent/client (the team delegates
// conducting the interview to the agent). The team keeps only the *result* types
// the agent posts back to team.sock.

type InterviewResult struct {
	RunID   string            `json:"run_id"`
	StaffID string            `json:"staff_id"`
	Answers []InterviewAnswer `json:"answers"`
}

type InterviewAnswer struct {
	TaskID       string `json:"task_id"`
	GithubItemID string `json:"github_item_id"`
	Title        string `json:"title"`
	Response     string `json:"response"`
}

// Coordinator orchestrates standup runs: dispatch interviews to the errands
// component, collect results via callback, sync GitHub, generate + post reports.
type Coordinator struct {
	e   *Engine
	ipc *commsclient.Client

	mu          sync.Mutex
	runs        map[string]*runState // runID -> in-flight run
	sentReports map[string]bool      // period:delegator:date -> sent (weekly/monthly dedup)
}

type runState struct {
	run      *store.StandupRun
	standup  *store.Standup
	expected int
	got      int
}

func NewCoordinator(e *Engine, c *commsclient.Client) *Coordinator {
	return &Coordinator{e: e, ipc: c, runs: map[string]*runState{}, sentReports: map[string]bool{}}
}

// Run executes one standup: create the run, then dispatch an interview per staff
// member who has active tasks. finalize() posts the report once all reply (or a
// watchdog times out).
func (co *Coordinator) Run(su *store.Standup) {
	date := time.Now().Format("2006-01-02")
	if existing, _ := co.e.s.RunOn(su.ID, date); existing != nil && existing.Status != store.RunFailed {
		return // already ran today
	}
	run, err := co.e.s.CreateStandupRun(su.ID, date)
	if err != nil {
		log.Printf("[standup] create run: %v", err)
		return
	}
	staff, _ := co.e.s.ListStaff(su.DelegatorID)

	type job struct {
		st    *store.Staff
		tasks []*store.Task
	}
	var jobs []job
	for _, st := range staff {
		tasks, _ := co.e.s.ActiveTasksFor(st.ID)
		if len(tasks) > 0 {
			jobs = append(jobs, job{st, tasks})
		}
	}
	if len(jobs) == 0 {
		_ = co.e.s.CompleteStandupRun(run.ID, "Daily Standup Summary\n\n(No active tasks to review.)")
		co.postReport(su, "Daily Standup Summary\n\n(No active tasks today.)")
		return
	}

	co.mu.Lock()
	co.runs[run.ID] = &runState{run: run, standup: su, expected: len(jobs)}
	co.mu.Unlock()

	for _, j := range jobs {
		co.dispatchInterview(run.ID, j.st, j.tasks)
	}
	// Watchdog: finalize with whatever arrived if interviews stall.
	go func(runID string) {
		time.Sleep(2 * time.Hour)
		co.finalize(runID, true)
	}(run.ID)
}

func (co *Coordinator) dispatchInterview(runID string, st *store.Staff, tasks []*store.Task) {
	spec := agentclient.InterviewSpec{
		RunID:    runID,
		StaffID:  st.ID,
		Target:   co.resolveTarget(st),
		Greeting: "Good morning. A few minutes for today's standup? Let's review your current tasks.",
		Closing:  "Thank you — I've recorded your updates.",
		Callback: "team.sock",
	}
	for _, t := range tasks {
		spec.Questions = append(spec.Questions, agentclient.InterviewQuestion{
			TaskID: t.ID, GithubItemID: t.GithubItemID, Title: t.Title,
			Prompt: "Task: " + t.Title + "\nHow is this task progressing?",
		})
	}
	// Delegate to the agent tier via agent/client — the agent conducts the
	// interview with its standup_interviewer persona and posts the result back to
	// team.sock. No prompt text or conversation logic lives in the team module.
	ac, err := agentclient.New()
	if err != nil {
		return
	}
	if err := ac.Interview(spec); err != nil {
		log.Printf("[standup] agent unreachable; can't interview %s: %v", st.TelegramUsername, err)
	}
}

// standupTarget addresses a staff member by their stored Telegram handle
// (numeric id preferred over @username).
func standupTarget(st *store.Staff) string {
	if st.TelegramUserID > 0 {
		return strconv.FormatInt(st.TelegramUserID, 10)
	}
	return st.TelegramUsername
}

// resolveTarget addresses a staff member, preferring the stored Telegram handle
// and falling back to the comms contacts directory (resolve by username → a
// platform handle) so recipients aren't hardcoded.
func (co *Coordinator) resolveTarget(st *store.Staff) string {
	if t := standupTarget(st); t != "" {
		return t
	}
	if co.ipc != nil && st.GithubUsername != "" {
		if cs, err := co.ipc.ResolveContact(st.GithubUsername); err == nil {
			for _, c := range cs {
				if addr := c.Address("telegram"); addr != "" {
					return addr
				}
			}
		}
	}
	return st.TelegramUsername
}

// OnResult is invoked from the team.sock handler when errands posts a completed
// interview. It stores updates, syncs GitHub + local task status, and finalizes
// the run when everyone has reported.
func (co *Coordinator) OnResult(res InterviewResult) {
	co.mu.Lock()
	rs := co.runs[res.RunID]
	co.mu.Unlock()
	if rs == nil {
		return
	}
	for _, a := range res.Answers {
		status, blocker := detectStatus(a.Response)
		_ = co.e.s.AddTaskUpdate(&store.TaskUpdate{
			StandupRunID: res.RunID, StaffMemberID: res.StaffID, GithubItemID: a.GithubItemID,
			TaskTitle: a.Title, StaffResponse: a.Response, DetectedStatus: status, Blocker: blocker,
		})
		if status != "" && a.TaskID != "" {
			// Keep the local task pool aligned with the standup result. A done
			// standup answer should free the assignee's slot just like finish task.
			if status == store.StatusDone {
				_, _ = co.e.s.FinishTask(a.TaskID, "system")
			} else if status == store.StatusAssigned || status == store.StatusBlocked || status == store.StatusReview {
				_ = co.e.s.SetTaskStatus(a.TaskID, status, "system")
			}
			co.syncGithub(a.GithubItemID, res.StaffID, status)
		}
	}
	co.mu.Lock()
	rs.got++
	done := rs.got >= rs.expected
	co.mu.Unlock()
	if done {
		co.finalize(res.RunID, false)
	}
}

func (co *Coordinator) syncGithub(itemID, staffID, status string) {
	if !githubEnabled || itemID == "" {
		return
	}
	st, _ := co.e.s.StaffByID(staffID)
	if st == nil {
		return
	}
	del, _ := co.e.s.DelegatorByID(st.DelegatorID)
	if del == nil {
		return
	}
	if proj := co.e.resolveProject(del); proj != nil {
		_ = proj.SetStatus(itemID, status)
		co.e.s.Audit("system", "github_updated", "task", itemID, map[string]any{"status": status})
	}
}

func (co *Coordinator) finalize(runID string, viaWatchdog bool) {
	co.mu.Lock()
	rs := co.runs[runID]
	if rs == nil {
		co.mu.Unlock()
		return
	}
	delete(co.runs, runID)
	co.mu.Unlock()

	updates, _ := co.e.s.TaskUpdatesForRun(runID)
	names := map[string]string{}
	for _, u := range updates {
		if _, ok := names[u.StaffMemberID]; !ok {
			if st, _ := co.e.s.StaffByID(u.StaffMemberID); st != nil {
				names[u.StaffMemberID] = st.TelegramUsername
			}
		}
	}
	report := generateReport(updates, names)
	_ = co.e.s.CompleteStandupRun(runID, report)
	co.postReport(rs.standup, report)

	// Daily PDF report → the delegator's creator.
	if del, err := co.e.s.DelegatorByID(rs.standup.DelegatorID); err == nil {
		co.sendDailyReport(del, report)
	}
}

// postReport sends the report to the standup's Telegram group via the daemon IPC.
func (co *Coordinator) postReport(su *store.Standup, report string) {
	if co.ipc == nil {
		return
	}
	if _, err := co.ipc.Send(su.TelegramGroup, report); err != nil {
		log.Printf("[standup] couldn't post report to %s: %v", su.TelegramGroup, err)
	}
}

// Register wires the standup tick onto the shared scheduler primitive (replacing
// the old hand-rolled sleep loop). Standups are dynamic (added/removed at
// runtime, each with its own time + timezone), so a single Interval job
// evaluates which are due each tick — the per-run RunOn guard prevents
// double-firing within the matching minute.
func (co *Coordinator) Register(s *scheduler.Scheduler) {
	s.Interval("standups", 30*time.Second, co.tick)
}

func (co *Coordinator) tick() {
	sus, err := co.e.s.ListStandups("")
	if err != nil {
		return
	}
	now := time.Now()
	for _, su := range sus {
		loc, err := time.LoadLocation(su.Timezone)
		if err != nil {
			loc = time.UTC
		}
		n := now.In(loc)
		var hh, mm int
		if _, e := fmt.Sscanf(su.ScheduleTime, "%d:%d", &hh, &mm); e != nil {
			continue
		}
		if n.Hour() == hh && n.Minute() == mm {
			go co.Run(su)
		}
	}
	// Weekly (Saturday) + monthly (1st) reports, at ~reportHour:00 local.
	if now.Hour() == reportHour && now.Minute() == 0 {
		co.runPeriodicReports(now)
	}
}
