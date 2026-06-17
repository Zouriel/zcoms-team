package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/Zouriel/zcoms-team/internal/github"
	"github.com/Zouriel/zcoms-team/internal/store"
)

// Engine adds per-actor multi-turn conversation state on top of the stateless
// command handlers, for the add/new/finish task flows. Handle returns the reply,
// whether the conversation continues (so the bridge keeps routing the actor's
// next message here), and any error.
type Engine struct {
	s        *store.Store
	mainUser string // the zcoms owner — treated as admin everywhere

	mu     sync.Mutex
	convos map[string]*conv // keyed by actor (@username)

	ghMu    sync.Mutex
	ghCache map[string]*github.Project // delegatorID -> resolved Projects v2 board
}

type conv struct {
	kind        string // add_tasks | add_priority | new_pick | finish_pick
	delegatorID string
	staffID     string
	titles      []string
	options     []*store.Task
}

func NewEngine(s *store.Store, mainUser string) *Engine {
	return &Engine{s: s, mainUser: normUser(mainUser), convos: map[string]*conv{}, ghCache: map[string]*github.Project{}}
}

func normUser(u string) string {
	u = strings.TrimSpace(u)
	if u != "" && !strings.HasPrefix(u, "@") {
		u = "@" + u
	}
	return u
}

func (e *Engine) isOwner(actor string) bool {
	a := normUser(actor)
	return a == e.mainUser || a == "@owner"
}

func (e *Engine) Handle(actor, text string) (reply string, cont bool, err error) {
	actor = normUser(actor)
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)

	e.mu.Lock()
	c := e.convos[actor]
	e.mu.Unlock()

	if lower == "cancel" || lower == "stop" {
		e.clear(actor)
		return "Cancelled.", false, nil
	}
	if c != nil {
		return e.step(actor, c, text)
	}

	switch {
	case lower == "add task" || lower == "add tasks":
		return e.startAddTask(actor)
	case lower == "new task":
		return e.startNewTask(actor)
	case lower == "finish task":
		return e.startFinishTask(actor)
	default:
		r, err := handleCommand(e.s, actor, text) // stateless CRUD + single-shot task forms
		return r, false, err
	}
}

func (e *Engine) clear(actor string) {
	e.mu.Lock()
	delete(e.convos, actor)
	e.mu.Unlock()
}

func (e *Engine) set(actor string, c *conv) {
	e.mu.Lock()
	e.convos[actor] = c
	e.mu.Unlock()
}

// --- add task ----------------------------------------------------------------

func (e *Engine) startAddTask(actor string) (string, bool, error) {
	del, err := e.resolveManageDelegator(actor)
	if err != nil {
		return "", false, err
	}
	e.set(actor, &conv{kind: "add_tasks", delegatorID: del.ID})
	return "Please send tasks, one per line. All tasks will share the same priority.", true, nil
}

func (e *Engine) step(actor string, c *conv, text string) (string, bool, error) {
	switch c.kind {
	case "add_tasks":
		var titles []string
		for _, ln := range strings.Split(text, "\n") {
			if t := strings.TrimSpace(ln); t != "" {
				titles = append(titles, t)
			}
		}
		if len(titles) == 0 {
			return "I didn't see any tasks. Send one per line, or 'cancel'.", true, nil
		}
		c.titles = titles
		c.kind = "add_priority"
		e.set(actor, c)
		return fmt.Sprintf("I found %d task(s).\n\nWhat priority should these use?\n  1 Critical\n  2 High\n  3 Medium\n  4 Low", len(titles)), true, nil

	case "add_priority":
		pri, ok := store.NormalizePriority(text)
		if !ok {
			return "Pick a priority: 1 Critical · 2 High · 3 Medium · 4 Low (or 'cancel').", true, nil
		}
		added, skipped := 0, 0
		for _, t := range c.titles {
			task, err := e.s.AddTask(c.delegatorID, t, pri, actor)
			if err != nil {
				return "", false, err
			}
			if task == nil {
				skipped++
			} else {
				added++
			}
		}
		e.clear(actor)
		msg := fmt.Sprintf("✅ Added %d task(s) at %s priority.", added, pri)
		if skipped > 0 {
			msg += fmt.Sprintf(" (%d duplicate(s) skipped.)", skipped)
		}
		return msg, false, nil

	case "new_pick":
		return e.pickNew(actor, c, text)

	case "finish_pick":
		return e.pickFinish(actor, c, text)
	}
	e.clear(actor)
	return "", false, fmt.Errorf("lost track of that conversation — please try again")
}

// --- new task (claim) --------------------------------------------------------

func (e *Engine) startNewTask(actor string) (string, bool, error) {
	st, del, err := e.resolveStaff(actor)
	if err != nil {
		return "", false, err
	}
	active, _ := e.s.CountActiveFor(st.ID)
	if active >= st.MaxActiveTasks {
		return fmt.Sprintf("You're at your task limit (%d). Finish one first with 'finish task'.", st.MaxActiveTasks), false, nil
	}
	opts, _ := e.s.AvailableTasks(del.ID, 3)
	if len(opts) == 0 {
		return "No unassigned tasks available right now. 🎉", false, nil
	}
	e.set(actor, &conv{kind: "new_pick", delegatorID: del.ID, staffID: st.ID, options: opts})
	return "Available tasks — reply with a number:\n" + numberedTasks(opts), true, nil
}

func (e *Engine) pickNew(actor string, c *conv, text string) (string, bool, error) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > len(c.options) {
		return fmt.Sprintf("Pick a number 1–%d, or 'cancel'.", len(c.options)), true, nil
	}
	task := c.options[n-1]
	assigned, err := e.s.AssignTask(task.ID, c.staffID, actor)
	if err != nil {
		e.clear(actor)
		return "", false, err
	}
	e.clear(actor)
	reply := fmt.Sprintf("✅ Assigned to you: %s", assigned.Title)
	if hook := e.onAssign(assigned, c.staffID); hook != "" {
		reply += "\n" + hook
	}
	return reply, false, nil
}

// --- finish task -------------------------------------------------------------

func (e *Engine) startFinishTask(actor string) (string, bool, error) {
	st, _, err := e.resolveStaff(actor)
	if err != nil {
		return "", false, err
	}
	active, _ := e.s.ActiveTasksFor(st.ID)
	if len(active) == 0 {
		return "You have no active tasks.", false, nil
	}
	e.set(actor, &conv{kind: "finish_pick", staffID: st.ID, delegatorID: st.DelegatorID, options: active})
	return "Your active tasks — reply with a number to finish:\n" + numberedTasks(active), true, nil
}

func (e *Engine) pickFinish(actor string, c *conv, text string) (string, bool, error) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > len(c.options) {
		return fmt.Sprintf("Pick a number 1–%d, or 'cancel'.", len(c.options)), true, nil
	}
	task := c.options[n-1]
	done, err := e.s.FinishTask(task.ID, actor)
	if err != nil {
		e.clear(actor)
		return "", false, err
	}
	reply := fmt.Sprintf("✅ Marked done: %s", done.Title)
	if hook := e.onFinish(done); hook != "" {
		reply += "\n" + hook
	}
	// Offer a replacement if under limit and tasks are available.
	st, _ := e.s.StaffByID(c.staffID)
	if st != nil {
		if cnt, _ := e.s.CountActiveFor(st.ID); cnt < st.MaxActiveTasks {
			if opts, _ := e.s.AvailableTasks(c.delegatorID, 3); len(opts) > 0 {
				e.set(actor, &conv{kind: "new_pick", delegatorID: c.delegatorID, staffID: c.staffID, options: opts})
				return reply + "\n\nWant another? Reply with a number:\n" + numberedTasks(opts), true, nil
			}
		}
	}
	e.clear(actor)
	return reply, false, nil
}

// --- helpers -----------------------------------------------------------------

// resolveStaff finds the single team membership for an actor (for claim/finish).
func (e *Engine) resolveStaff(actor string) (*store.Staff, *store.Delegator, error) {
	sts, _ := e.s.StaffByTelegram(actor)
	switch len(sts) {
	case 0:
		return nil, nil, fmt.Errorf("you're not on any team — ask an admin to add you")
	case 1:
		del, err := e.s.DelegatorByID(sts[0].DelegatorID)
		return sts[0], del, err
	default:
		return nil, nil, fmt.Errorf("you're on multiple teams; use the CLI form: zc team task new <delegator> %s", actor)
	}
}

// resolveManageDelegator finds the single delegator where the actor may add
// tasks (admin/lead), or treats the owner specially.
func (e *Engine) resolveManageDelegator(actor string) (*store.Delegator, error) {
	if e.isOwner(actor) {
		dels, _ := e.s.ListDelegators()
		if len(dels) == 1 {
			return dels[0], nil
		}
		return nil, fmt.Errorf("specify the project: zc team task add <delegator> <priority> <title>")
	}
	sts, _ := e.s.StaffByTelegram(actor)
	var managed []*store.Staff
	for _, st := range sts {
		if st.Role == store.RoleAdmin || st.Role == store.RoleLead {
			managed = append(managed, st)
		}
	}
	switch len(managed) {
	case 0:
		return nil, fmt.Errorf("only an admin or lead can add tasks")
	case 1:
		return e.s.DelegatorByID(managed[0].DelegatorID)
	default:
		return nil, fmt.Errorf("you lead multiple projects; use: zc team task add <delegator> <priority> <title>")
	}
}

func numberedTasks(ts []*store.Task) string {
	var b strings.Builder
	for i, t := range ts {
		fmt.Fprintf(&b, "  %d %s", i+1, t.Title)
		if t.Priority != "" {
			fmt.Fprintf(&b, "  [%s]", t.Priority)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
