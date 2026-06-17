package main

import (
	"github.com/Zouriel/zcoms-team/internal/github"
	"github.com/Zouriel/zcoms-team/internal/store"
)

// githubEnabled gates the GitHub Projects v2 sync. Disabled for now — zc-team
// relies purely on its SQLite database for assignment + standups. Flip to true
// (and the github package is ready) to re-enable Projects sync.
const githubEnabled = false

// resolveProject resolves (and caches) a delegator's Projects v2 board, recording
// the resolved node id back onto the delegator the first time.
func (e *Engine) resolveProject(del *store.Delegator) *github.Project {
	if !del.HasGitHub() {
		return nil
	}
	e.ghMu.Lock()
	defer e.ghMu.Unlock()
	if p, ok := e.ghCache[del.ID]; ok {
		return p
	}
	p, err := github.Resolve(del.GithubOwner, del.GithubProjectNumber)
	if err != nil {
		return nil
	}
	e.ghCache[del.ID] = p
	if del.GithubProjectID == "" {
		_ = e.s.SetDelegatorProjectID(del.ID, p.ID)
	}
	return p
}

// onAssign creates/locates the task's GitHub item and sets it In Progress.
// Best-effort: returns a short note (or "" / a soft warning) — never blocks the
// local assignment.
func (e *Engine) onAssign(task *store.Task, staffID string) string {
	if !githubEnabled || !github.Available() {
		return ""
	}
	del, err := e.s.DelegatorByID(task.DelegatorID)
	if err != nil {
		return ""
	}
	proj := e.resolveProject(del)
	if proj == nil {
		return "⚠️ GitHub sync skipped (couldn't resolve the project)"
	}
	itemID := task.GithubItemID
	if itemID == "" {
		body := ""
		if st, _ := e.s.StaffByID(staffID); st != nil && st.GithubUsername != "" {
			body = "Assigned to @" + st.GithubUsername
		}
		id, err := github.CreateDraftItem(proj.ID, task.Title, body)
		if err != nil {
			return "⚠️ GitHub: couldn't create item (" + err.Error() + ")"
		}
		itemID = id
		_ = e.s.SetTaskGithubItem(task.ID, id)
	}
	_ = proj.SetPriority(itemID, task.Priority)
	if err := proj.SetStatus(itemID, store.StatusAssigned); err != nil {
		return "🔗 GitHub item ready (status update skipped: " + err.Error() + ")"
	}
	e.s.Audit("system", "github_updated", "task", task.ID, map[string]any{"status": "in progress"})
	return "🔗 GitHub: In Progress"
}

// onFinish sets the task's GitHub item to Done.
func (e *Engine) onFinish(task *store.Task) string {
	if !githubEnabled || !github.Available() || task.GithubItemID == "" {
		return ""
	}
	del, err := e.s.DelegatorByID(task.DelegatorID)
	if err != nil {
		return ""
	}
	proj := e.resolveProject(del)
	if proj == nil {
		return ""
	}
	if err := proj.SetStatus(task.GithubItemID, store.StatusDone); err != nil {
		return ""
	}
	e.s.Audit("system", "github_updated", "task", task.ID, map[string]any{"status": "done"})
	return "🔗 GitHub: Done"
}
