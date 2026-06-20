package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task priorities (highest → lowest) and the ordering rank.
const (
	PriorityCritical = "critical"
	PriorityHigh     = "high"
	PriorityMedium   = "medium"
	PriorityLow      = "low"
)

// Task statuses. They drive the GitHub status mapping (see internal/github).
const (
	StatusUnassigned = "unassigned"
	StatusAssigned   = "assigned"
	StatusBlocked    = "blocked"
	StatusReview     = "review"
	StatusDone       = "done"
)

func priorityRank(p string) int {
	switch strings.ToLower(p) {
	case PriorityCritical:
		return 0
	case PriorityHigh:
		return 1
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 3
	}
	return 4
}

// NormalizePriority maps user input ("1".."4" or names) to a canonical priority.
func NormalizePriority(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "critical":
		return PriorityCritical, true
	case "2", "high":
		return PriorityHigh, true
	case "3", "medium":
		return PriorityMedium, true
	case "4", "low":
		return PriorityLow, true
	}
	return "", false
}

// normalizeTitle lowercases + collapses whitespace for dedup within a delegator.
func normalizeTitle(t string) string {
	return strings.Join(strings.Fields(strings.ToLower(t)), " ")
}

type Task struct {
	ID            string
	DelegatorID   string
	Title         string
	Priority      string
	Status        string
	GithubItemID  string
	AssignedStaff string
	CreatedBy     string
	AssignedAt    sql.NullTime
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// activeStatuses are the statuses that count toward a staff member's task limit.
var activeStatuses = []string{StatusAssigned, StatusBlocked, StatusReview}

// AddTask inserts a task (status unassigned), skipping a duplicate of any task
// in the delegator that isn't Done. Returns the task, or (nil,nil) if a
// duplicate was skipped.
func (s *Store) AddTask(delegatorID, title, priority, createdBy string) (*Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("empty task title")
	}
	norm := normalizeTitle(title)
	var existing int
	_ = s.db.QueryRow(
		`SELECT COUNT(*) FROM task_pool WHERE delegator_id=? AND normalized_title=? AND status!=?`,
		delegatorID, norm, StatusDone,
	).Scan(&existing)
	if existing > 0 {
		return nil, nil // duplicate of a live task — skip silently
	}
	t := &Task{
		ID: NewID(), DelegatorID: delegatorID, Title: title, Priority: priority,
		Status: StatusUnassigned, CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.db.Exec(
		`INSERT INTO task_pool(id,delegator_id,title,normalized_title,priority,status,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		t.ID, delegatorID, title, norm, priority, StatusUnassigned, createdBy, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	s.Audit(createdBy, "task_added", "task", t.ID, map[string]any{"title": title, "priority": priority})
	return t, nil
}

const taskCols = `id,delegator_id,title,priority,status,github_item_id,assigned_staff_id,created_by,assigned_at,created_at,updated_at`

func scanTask(row interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	var gh, staff sql.NullString
	if err := row.Scan(&t.ID, &t.DelegatorID, &t.Title, &t.Priority, &t.Status, &gh, &staff, &t.CreatedBy, &t.AssignedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.GithubItemID, t.AssignedStaff = gh.String, staff.String
	return &t, nil
}

// AvailableTasks returns the top unassigned tasks, ordered Critical→Low then oldest.
func (s *Store) AvailableTasks(delegatorID string, limit int) ([]*Task, error) {
	rows, err := s.db.Query(
		`SELECT `+taskCols+` FROM task_pool
		 WHERE delegator_id=? AND status=?
		 ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,
		          created_at ASC
		 LIMIT ?`,
		delegatorID, StatusUnassigned, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

// ActiveTasksFor returns a staff member's in-flight tasks (assigned/blocked/review).
func (s *Store) ActiveTasksFor(staffID string) ([]*Task, error) {
	rows, err := s.db.Query(
		`SELECT `+taskCols+` FROM task_pool WHERE assigned_staff_id=? AND status IN ('assigned','blocked','review') ORDER BY assigned_at`,
		staffID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

func (s *Store) CountActiveFor(staffID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM task_pool WHERE assigned_staff_id=? AND status IN ('assigned','blocked','review')`, staffID).Scan(&n)
	return n, err
}

func (s *Store) TaskByID(id string) (*Task, error) {
	row := s.db.QueryRow(`SELECT `+taskCols+` FROM task_pool WHERE id=?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// AssignTask claims an unassigned task for a staff member.
func (s *Store) AssignTask(taskID, staffID, actor string) (*Task, error) {
	res, err := s.db.Exec(
		`UPDATE task_pool SET status=?, assigned_staff_id=?, assigned_at=?, updated_at=? WHERE id=? AND status=?`,
		StatusAssigned, staffID, now(), now(), taskID, StatusUnassigned,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("that task was just taken — try another")
	}
	s.Audit(actor, "task_assigned", "task", taskID, map[string]any{"staff": staffID})
	return s.TaskByID(taskID)
}

// FinishTask marks an assigned task Done.
func (s *Store) FinishTask(taskID, actor string) (*Task, error) {
	if _, err := s.db.Exec(`UPDATE task_pool SET status=?, updated_at=? WHERE id=?`, StatusDone, now(), taskID); err != nil {
		return nil, err
	}
	s.Audit(actor, "task_completed", "task", taskID, nil)
	return s.TaskByID(taskID)
}

// UnassignTask returns an active task to the unassigned pool.
func (s *Store) UnassignTask(taskID, staffID, actor string) (*Task, error) {
	res, err := s.db.Exec(
		`UPDATE task_pool SET status=?, assigned_staff_id=NULL, assigned_at=NULL, updated_at=?
		 WHERE id=? AND assigned_staff_id=? AND status IN ('assigned','blocked','review')`,
		StatusUnassigned, now(), taskID, staffID,
	)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("that task is no longer assigned to you")
	}
	s.Audit(actor, "task_unassigned", "task", taskID, map[string]any{"staff": staffID})
	return s.TaskByID(taskID)
}

// SetTaskStatus updates a task's status (used by standups: blocked/review/etc.).
func (s *Store) SetTaskStatus(taskID, status, actor string) error {
	_, err := s.db.Exec(`UPDATE task_pool SET status=?, updated_at=? WHERE id=?`, status, now(), taskID)
	return err
}

// SetTaskGithubItem records the GitHub item id for a task (Phase 3).
func (s *Store) SetTaskGithubItem(taskID, itemID string) error {
	_, err := s.db.Exec(`UPDATE task_pool SET github_item_id=?, updated_at=? WHERE id=?`, itemID, now(), taskID)
	return err
}

// StaffByTelegram returns all active staff records for a telegram username,
// across delegators (a person can be staff in more than one project).
func (s *Store) StaffByTelegram(tgUser string) ([]*Staff, error) {
	user := normUser(tgUser)
	var rows *sql.Rows
	var err error
	if id, ok := telegramActorID(user); ok {
		rows, err = s.db.Query(`SELECT `+staffCols+` FROM staff_members WHERE telegram_user_id=? AND active=1`, id)
	} else {
		rows, err = s.db.Query(`SELECT `+staffCols+` FROM staff_members WHERE lower(telegram_username)=? AND active=1`, user)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Staff
	for rows.Next() {
		st, err := scanStaff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) StaffByID(id string) (*Staff, error) {
	row := s.db.QueryRow(`SELECT `+staffCols+` FROM staff_members WHERE id=?`, id)
	st, err := scanStaff(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return st, err
}

func (s *Store) DelegatorByID(id string) (*Delegator, error) {
	row := s.db.QueryRow(`SELECT `+delegatorCols+` FROM delegator_agents WHERE id=?`, id)
	d, err := scanDelegator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func collectTasks(rows *sql.Rows) ([]*Task, error) {
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
