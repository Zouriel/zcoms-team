package store

import (
	"database/sql"
	"errors"
	"time"
)

// Standup run statuses.
const (
	RunRunning   = "running"
	RunCompleted = "completed"
	RunFailed    = "failed"
)

type StandupRun struct {
	ID             string
	StandupAgentID string
	RunDate        string // YYYY-MM-DD
	StartedAt      time.Time
	CompletedAt    sql.NullTime
	Status         string
	GroupReport    string
	ErrorMessage   string
}

type TaskUpdate struct {
	ID             string
	StandupRunID   string
	StaffMemberID  string
	GithubItemID   string
	TaskTitle      string
	StaffResponse  string
	NormalizedSum  string
	DetectedStatus string
	Blocker        string
	CreatedAt      time.Time
}

func (s *Store) CreateStandupRun(agentID, runDate string) (*StandupRun, error) {
	r := &StandupRun{ID: NewID(), StandupAgentID: agentID, RunDate: runDate, StartedAt: now(), Status: RunRunning}
	_, err := s.db.Exec(
		`INSERT INTO standup_runs(id,standup_agent_id,run_date,started_at,status) VALUES(?,?,?,?,?)`,
		r.ID, agentID, runDate, r.StartedAt, RunRunning,
	)
	if err != nil {
		return nil, err
	}
	s.Audit("system", "standup_started", "standup_run", r.ID, map[string]any{"agent": agentID, "date": runDate})
	return r, nil
}

func (s *Store) CompleteStandupRun(runID, report string) error {
	if _, err := s.db.Exec(`UPDATE standup_runs SET status=?, completed_at=?, group_report=? WHERE id=?`, RunCompleted, now(), report, runID); err != nil {
		return err
	}
	s.Audit("system", "standup_completed", "standup_run", runID, nil)
	return nil
}

func (s *Store) FailStandupRun(runID, msg string) error {
	_, err := s.db.Exec(`UPDATE standup_runs SET status=?, completed_at=?, error_message=? WHERE id=?`, RunFailed, now(), msg, runID)
	return err
}

// RunOn returns the run for an agent on a given date, if any (used to avoid
// double-running and for `standup report <date>`).
func (s *Store) RunOn(agentID, date string) (*StandupRun, error) {
	row := s.db.QueryRow(
		`SELECT id,standup_agent_id,run_date,started_at,completed_at,status,COALESCE(group_report,''),COALESCE(error_message,'')
		 FROM standup_runs WHERE standup_agent_id=? AND run_date=? ORDER BY started_at DESC LIMIT 1`,
		agentID, date,
	)
	return scanRun(row)
}

func scanRun(row interface{ Scan(...any) error }) (*StandupRun, error) {
	var r StandupRun
	if err := row.Scan(&r.ID, &r.StandupAgentID, &r.RunDate, &r.StartedAt, &r.CompletedAt, &r.Status, &r.GroupReport, &r.ErrorMessage); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) AddTaskUpdate(u *TaskUpdate) error {
	if u.ID == "" {
		u.ID = NewID()
	}
	u.CreatedAt = now()
	_, err := s.db.Exec(
		`INSERT INTO standup_task_updates(id,standup_run_id,staff_member_id,github_item_id,task_title,staff_response,normalized_summary,detected_status,blocker,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.StandupRunID, u.StaffMemberID, nullStr(u.GithubItemID), u.TaskTitle, u.StaffResponse,
		nullStr(u.NormalizedSum), nullStr(u.DetectedStatus), nullStr(u.Blocker), u.CreatedAt,
	)
	return err
}

// TaskUpdatesForRun returns all collected updates for a run, oldest first.
func (s *Store) TaskUpdatesForRun(runID string) ([]*TaskUpdate, error) {
	rows, err := s.db.Query(
		`SELECT id,standup_run_id,staff_member_id,COALESCE(github_item_id,''),task_title,staff_response,COALESCE(normalized_summary,''),COALESCE(detected_status,''),COALESCE(blocker,''),created_at
		 FROM standup_task_updates WHERE standup_run_id=? ORDER BY created_at`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TaskUpdate
	for rows.Next() {
		var u TaskUpdate
		if err := rows.Scan(&u.ID, &u.StandupRunID, &u.StaffMemberID, &u.GithubItemID, &u.TaskTitle, &u.StaffResponse, &u.NormalizedSum, &u.DetectedStatus, &u.Blocker, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
