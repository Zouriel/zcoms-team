// Package db opens the zc-team SQLite database and applies migrations. The DB is
// pure-Go (modernc.org/sqlite), so the component stays a cgo-free prebuilt binary.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Zouriel/zcoms-sdk/agent"
	_ "modernc.org/sqlite"
)

// DefaultPath returns ~/.config/zcoms/zc-team/team.db, creating the directory.
func DefaultPath() (string, error) {
	base, err := agent.DefaultAppDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "zc-team")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "team.db"), nil
}

// Open opens (creating if needed) the team database at path and runs migrations.
func Open(path string) (*sql.DB, error) {
	// _pragma busy_timeout so concurrent writers (scheduler + command socket)
	// don't fail immediately on a momentary lock.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1) // SQLite single-writer; serialize to avoid lock churn
	if err := migrate(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// migrations are applied in order; schema_version records how many have run.
var migrations = []string{
	`CREATE TABLE delegator_agents (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		github_owner TEXT NOT NULL,
		github_project_number INTEGER NOT NULL,
		github_project_id TEXT,
		created_by TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		is_active BOOLEAN NOT NULL DEFAULT 1
	);`,
	`CREATE TABLE standup_agents (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		delegator_id TEXT NOT NULL,
		telegram_group TEXT NOT NULL,
		schedule_time TEXT NOT NULL,
		timezone TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT 1,
		created_by TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		FOREIGN KEY(delegator_id) REFERENCES delegator_agents(id)
	);`,
	`CREATE TABLE staff_members (
		id TEXT PRIMARY KEY,
		delegator_id TEXT NOT NULL,
		telegram_username TEXT NOT NULL,
		github_username TEXT NOT NULL,
		role TEXT NOT NULL,
		max_active_tasks INTEGER NOT NULL DEFAULT 2,
		active BOOLEAN NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		FOREIGN KEY(delegator_id) REFERENCES delegator_agents(id)
	);`,
	`CREATE TABLE task_pool (
		id TEXT PRIMARY KEY,
		delegator_id TEXT NOT NULL,
		title TEXT NOT NULL,
		normalized_title TEXT NOT NULL,
		priority TEXT NOT NULL,
		status TEXT NOT NULL,
		github_item_id TEXT,
		assigned_staff_id TEXT,
		created_by TEXT NOT NULL,
		assigned_at DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		FOREIGN KEY(delegator_id) REFERENCES delegator_agents(id)
	);`,
	`CREATE TABLE standup_runs (
		id TEXT PRIMARY KEY,
		standup_agent_id TEXT NOT NULL,
		run_date DATE NOT NULL,
		started_at DATETIME NOT NULL,
		completed_at DATETIME,
		status TEXT NOT NULL,
		group_report TEXT,
		error_message TEXT,
		FOREIGN KEY(standup_agent_id) REFERENCES standup_agents(id)
	);`,
	`CREATE TABLE standup_task_updates (
		id TEXT PRIMARY KEY,
		standup_run_id TEXT NOT NULL,
		staff_member_id TEXT NOT NULL,
		github_item_id TEXT,
		task_title TEXT NOT NULL,
		staff_response TEXT NOT NULL,
		normalized_summary TEXT,
		detected_status TEXT,
		blocker TEXT,
		created_at DATETIME NOT NULL,
		FOREIGN KEY(standup_run_id) REFERENCES standup_runs(id)
	);`,
	`CREATE TABLE audit_logs (
		id TEXT PRIMARY KEY,
		actor TEXT NOT NULL,
		action TEXT NOT NULL,
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		details_json TEXT,
		created_at DATETIME NOT NULL
	);`,
	// Helpful indexes for the hot lookups.
	`CREATE INDEX idx_task_pool_delegator_status ON task_pool(delegator_id, status, priority);
	 CREATE INDEX idx_staff_delegator ON staff_members(delegator_id, telegram_username);
	 CREATE INDEX idx_standup_runs_agent_date ON standup_runs(standup_agent_id, run_date);`,
	`ALTER TABLE delegator_agents ADD COLUMN local_only BOOLEAN NOT NULL DEFAULT 0;
	 UPDATE delegator_agents SET local_only=1 WHERE github_project_number=0 AND (github_owner='' OR lower(github_owner)='local');`,
	`ALTER TABLE staff_members ADD COLUMN telegram_user_id INTEGER;
	 CREATE INDEX idx_staff_telegram_user_id ON staff_members(telegram_user_id);`,
}

func migrate(d *sql.DB) error {
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	row := d.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	if err := row.Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := d.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES(?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
