package main

import "github.com/Zouriel/zcoms-team/internal/store"

// onAssign / onFinish are the GitHub Projects sync hooks fired when a task is
// claimed or finished. Phase 3 fills these in (create/locate item, assign user,
// set status, add comment); for now they are no-ops returning an optional note
// to append to the user-facing reply.
func (e *Engine) onAssign(task *store.Task, staffID string) string {
	return ""
}

func (e *Engine) onFinish(task *store.Task) string {
	return ""
}
