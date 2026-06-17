package store

import "time"

// CompletedBetween returns tasks marked Done in [from, to) (done is terminal, so
// updated_at is effectively the completion time).
func (s *Store) CompletedBetween(delegatorID string, from, to time.Time) ([]*Task, error) {
	rows, err := s.db.Query(
		`SELECT `+taskCols+` FROM task_pool
		 WHERE delegator_id=? AND status=? AND updated_at>=? AND updated_at<?
		 ORDER BY updated_at`,
		delegatorID, StatusDone, from.UTC(), to.UTC(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

// TasksWhere returns a delegator's tasks in any of the given statuses (current
// snapshot), ordered by priority then age.
func (s *Store) TasksWhere(delegatorID string, statuses ...string) ([]*Task, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	q := `SELECT ` + taskCols + ` FROM task_pool WHERE delegator_id=? AND status IN (`
	args := []any{delegatorID}
	for i, st := range statuses {
		if i > 0 {
			q += ","
		}
		q += "?"
		args = append(args, st)
	}
	q += `) ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END, created_at`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

// CountRunsBetween counts standup runs for a delegator's standups in a window
// (for report participation stats).
func (s *Store) CountRunsBetween(delegatorID string, from, to time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM standup_runs r
		 JOIN standup_agents a ON a.id=r.standup_agent_id
		 WHERE a.delegator_id=? AND r.started_at>=? AND r.started_at<?`,
		delegatorID, from.UTC(), to.UTC(),
	).Scan(&n)
	return n, err
}
