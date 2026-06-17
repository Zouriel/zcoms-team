// Package store is the typed data access layer over the zc-team SQLite database:
// models + CRUD for delegators, standup agents, and staff, plus audit logging.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store wraps the database with typed operations.
type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// NewID returns a short random hex id used as a primary key.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func now() time.Time { return time.Now().UTC() }

// --- models ------------------------------------------------------------------

type Delegator struct {
	ID                  string
	Name                string
	GithubOwner         string
	GithubProjectNumber int
	GithubProjectID     string
	LocalOnly           bool
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	IsActive            bool
}

func (d *Delegator) HasGitHub() bool {
	return d != nil && !d.LocalOnly && strings.TrimSpace(d.GithubOwner) != "" && d.GithubProjectNumber > 0
}

type Standup struct {
	ID            string
	Name          string
	DelegatorID   string
	TelegramGroup string
	ScheduleTime  string
	Timezone      string
	Enabled       bool
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Staff struct {
	ID               string
	DelegatorID      string
	TelegramUsername string
	GithubUsername   string
	Role             string
	MaxActiveTasks   int
	Active           bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Roles.
const (
	RoleAdmin = "admin"
	RoleLead  = "lead"
	RoleStaff = "staff"
)

func ValidRole(r string) bool {
	switch strings.ToLower(r) {
	case RoleAdmin, RoleLead, RoleStaff:
		return true
	}
	return false
}

var ErrNotFound = errors.New("not found")

// --- audit -------------------------------------------------------------------

// Audit records one action. details may be nil.
func (s *Store) Audit(actor, action, entityType, entityID string, details any) {
	var dj sql.NullString
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			dj = sql.NullString{String: string(b), Valid: true}
		}
	}
	_, _ = s.db.Exec(
		`INSERT INTO audit_logs(id, actor, action, entity_type, entity_id, details_json, created_at) VALUES(?,?,?,?,?,?,?)`,
		NewID(), actor, action, entityType, entityID, dj, now(),
	)
}

// --- delegators --------------------------------------------------------------

func (s *Store) CreateDelegator(name, owner string, projectNumber int, createdBy string) (*Delegator, error) {
	return s.createDelegator(name, owner, projectNumber, false, createdBy)
}

func (s *Store) CreateLocalDelegator(name, createdBy string) (*Delegator, error) {
	return s.createDelegator(name, "", 0, true, createdBy)
}

func (s *Store) createDelegator(name, owner string, projectNumber int, localOnly bool, createdBy string) (*Delegator, error) {
	d := &Delegator{
		ID: NewID(), Name: name, GithubOwner: owner, GithubProjectNumber: projectNumber,
		LocalOnly: localOnly, CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(), IsActive: true,
	}
	_, err := s.db.Exec(
		`INSERT INTO delegator_agents(id,name,github_owner,github_project_number,created_by,created_at,updated_at,is_active,local_only)
		 VALUES(?,?,?,?,?,?,?,1,?)`,
		d.ID, d.Name, d.GithubOwner, d.GithubProjectNumber, d.CreatedBy, d.CreatedAt, d.UpdatedAt, d.LocalOnly,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("a delegator named %q already exists", name)
		}
		return nil, err
	}
	details := map[string]any{"name": name}
	if localOnly {
		details["local_only"] = true
	} else {
		details["owner"] = owner
		details["project"] = projectNumber
	}
	s.Audit(createdBy, "delegator_created", "delegator", d.ID, details)
	return d, nil
}

func scanDelegator(row interface{ Scan(...any) error }) (*Delegator, error) {
	var d Delegator
	var pid sql.NullString
	if err := row.Scan(&d.ID, &d.Name, &d.GithubOwner, &d.GithubProjectNumber, &pid, &d.LocalOnly, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.IsActive); err != nil {
		return nil, err
	}
	d.GithubProjectID = pid.String
	return &d, nil
}

const delegatorCols = `id,name,github_owner,github_project_number,github_project_id,local_only,created_by,created_at,updated_at,is_active`

func (s *Store) DelegatorByName(name string) (*Delegator, error) {
	row := s.db.QueryRow(`SELECT `+delegatorCols+` FROM delegator_agents WHERE name=? AND is_active=1`, name)
	d, err := scanDelegator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) ListDelegators() ([]*Delegator, error) {
	rows, err := s.db.Query(`SELECT ` + delegatorCols + ` FROM delegator_agents WHERE is_active=1 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Delegator
	for rows.Next() {
		d, err := scanDelegator(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetDelegatorProjectID records the resolved GitHub Project node id (Phase 3).
func (s *Store) SetDelegatorProjectID(id, projectID string) error {
	_, err := s.db.Exec(`UPDATE delegator_agents SET github_project_id=?, updated_at=? WHERE id=?`, projectID, now(), id)
	return err
}

func (s *Store) DeleteDelegator(name, actor string) error {
	d, err := s.DelegatorByName(name)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE delegator_agents SET is_active=0, updated_at=? WHERE id=?`, now(), d.ID); err != nil {
		return err
	}
	s.Audit(actor, "delegator_deleted", "delegator", d.ID, map[string]any{"name": name})
	return nil
}

// --- standups ----------------------------------------------------------------

func (s *Store) CreateStandup(name, delegatorID, group, scheduleTime, timezone, createdBy string) (*Standup, error) {
	st := &Standup{
		ID: NewID(), Name: name, DelegatorID: delegatorID, TelegramGroup: group,
		ScheduleTime: scheduleTime, Timezone: timezone, Enabled: true,
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.db.Exec(
		`INSERT INTO standup_agents(id,name,delegator_id,telegram_group,schedule_time,timezone,enabled,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,1,?,?,?)`,
		st.ID, st.Name, st.DelegatorID, st.TelegramGroup, st.ScheduleTime, st.Timezone, st.CreatedBy, st.CreatedAt, st.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("a standup named %q already exists", name)
		}
		return nil, err
	}
	s.Audit(createdBy, "standup_created", "standup", st.ID, map[string]any{"name": name, "group": group, "time": scheduleTime})
	return st, nil
}

const standupCols = `id,name,delegator_id,telegram_group,schedule_time,timezone,enabled,created_by,created_at,updated_at`

func scanStandup(row interface{ Scan(...any) error }) (*Standup, error) {
	var st Standup
	if err := row.Scan(&st.ID, &st.Name, &st.DelegatorID, &st.TelegramGroup, &st.ScheduleTime, &st.Timezone, &st.Enabled, &st.CreatedBy, &st.CreatedAt, &st.UpdatedAt); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) StandupByName(name string) (*Standup, error) {
	row := s.db.QueryRow(`SELECT `+standupCols+` FROM standup_agents WHERE name=? AND enabled=1`, name)
	st, err := scanStandup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return st, err
}

// ListStandups returns enabled standups; pass "" for all delegators.
func (s *Store) ListStandups(delegatorID string) ([]*Standup, error) {
	q := `SELECT ` + standupCols + ` FROM standup_agents WHERE enabled=1`
	args := []any{}
	if delegatorID != "" {
		q += ` AND delegator_id=?`
		args = append(args, delegatorID)
	}
	q += ` ORDER BY name`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Standup
	for rows.Next() {
		st, err := scanStandup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) DeleteStandup(name, actor string) error {
	st, err := s.StandupByName(name)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE standup_agents SET enabled=0, updated_at=? WHERE id=?`, now(), st.ID); err != nil {
		return err
	}
	s.Audit(actor, "standup_deleted", "standup", st.ID, map[string]any{"name": name})
	return nil
}

// --- staff -------------------------------------------------------------------

func normUser(u string) string {
	u = strings.TrimSpace(u)
	if !strings.HasPrefix(u, "@") {
		u = "@" + u
	}
	return u
}

func (s *Store) AddStaff(delegatorID, tgUser, ghUser, role string, limit int, actor string) (*Staff, error) {
	role = strings.ToLower(role)
	if !ValidRole(role) {
		return nil, fmt.Errorf("role must be admin, lead, or staff")
	}
	tgUser = normUser(tgUser)
	// Reactivate if a soft-deleted member with the same handle exists.
	if existing, err := s.StaffByUser(delegatorID, tgUser); err == nil {
		_, err := s.db.Exec(`UPDATE staff_members SET github_username=?, role=?, max_active_tasks=?, active=1, updated_at=? WHERE id=?`,
			ghUser, role, limit, now(), existing.ID)
		if err != nil {
			return nil, err
		}
		existing.GithubUsername, existing.Role, existing.MaxActiveTasks, existing.Active = ghUser, role, limit, true
		s.Audit(actor, "staff_added", "staff", existing.ID, map[string]any{"telegram": tgUser, "github": ghUser, "role": role, "reactivated": true})
		return existing, nil
	}
	st := &Staff{
		ID: NewID(), DelegatorID: delegatorID, TelegramUsername: tgUser, GithubUsername: ghUser,
		Role: role, MaxActiveTasks: limit, Active: true, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.db.Exec(
		`INSERT INTO staff_members(id,delegator_id,telegram_username,github_username,role,max_active_tasks,active,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,1,?,?)`,
		st.ID, st.DelegatorID, st.TelegramUsername, st.GithubUsername, st.Role, st.MaxActiveTasks, st.CreatedAt, st.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	s.Audit(actor, "staff_added", "staff", st.ID, map[string]any{"telegram": tgUser, "github": ghUser, "role": role})
	return st, nil
}

const staffCols = `id,delegator_id,telegram_username,github_username,role,max_active_tasks,active,created_at,updated_at`

func scanStaff(row interface{ Scan(...any) error }) (*Staff, error) {
	var st Staff
	if err := row.Scan(&st.ID, &st.DelegatorID, &st.TelegramUsername, &st.GithubUsername, &st.Role, &st.MaxActiveTasks, &st.Active, &st.CreatedAt, &st.UpdatedAt); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) StaffByUser(delegatorID, tgUser string) (*Staff, error) {
	row := s.db.QueryRow(`SELECT `+staffCols+` FROM staff_members WHERE delegator_id=? AND telegram_username=? AND active=1`, delegatorID, normUser(tgUser))
	st, err := scanStaff(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return st, err
}

func (s *Store) ListStaff(delegatorID string) ([]*Staff, error) {
	rows, err := s.db.Query(`SELECT `+staffCols+` FROM staff_members WHERE delegator_id=? AND active=1 ORDER BY telegram_username`, delegatorID)
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

func (s *Store) RemoveStaff(delegatorID, tgUser, actor string) error {
	st, err := s.StaffByUser(delegatorID, tgUser)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE staff_members SET active=0, updated_at=? WHERE id=?`, now(), st.ID); err != nil {
		return err
	}
	s.Audit(actor, "staff_removed", "staff", st.ID, map[string]any{"telegram": st.TelegramUsername})
	return nil
}

func (s *Store) SetStaffRole(delegatorID, tgUser, role, actor string) error {
	role = strings.ToLower(role)
	if !ValidRole(role) {
		return fmt.Errorf("role must be admin, lead, or staff")
	}
	st, err := s.StaffByUser(delegatorID, tgUser)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE staff_members SET role=?, updated_at=? WHERE id=?`, role, now(), st.ID); err != nil {
		return err
	}
	s.Audit(actor, "role_changed", "staff", st.ID, map[string]any{"telegram": st.TelegramUsername, "role": role})
	return nil
}

func (s *Store) SetStaffLimit(delegatorID, tgUser string, limit int, actor string) error {
	st, err := s.StaffByUser(delegatorID, tgUser)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE staff_members SET max_active_tasks=?, updated_at=? WHERE id=?`, limit, now(), st.ID); err != nil {
		return err
	}
	s.Audit(actor, "limit_changed", "staff", st.ID, map[string]any{"telegram": st.TelegramUsername, "limit": limit})
	return nil
}
