package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zouriel/zcoms-team/internal/db"
	"github.com/Zouriel/zcoms-team/internal/store"
)

func TestEngineResolvesTelegramUserIDActor(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	s := store.New(d)

	delegator, err := s.CreateLocalDelegator("hems", "@owner")
	if err != nil {
		t.Fatalf("delegator: %v", err)
	}
	staff, err := s.AddStaff(delegator.ID, "@ahm3dyaseen", "ahm3dyaseen", store.RoleLead, 5, "@owner")
	if err != nil {
		t.Fatalf("staff: %v", err)
	}
	if _, err := d.Exec(`UPDATE staff_members SET telegram_user_id=? WHERE id=?`, int64(1098392910), staff.ID); err != nil {
		t.Fatalf("set telegram_user_id: %v", err)
	}
	if _, err := s.AddTask(delegator.ID, "Check actor lookup", store.PriorityHigh, "@owner"); err != nil {
		t.Fatalf("task: %v", err)
	}

	e := NewEngine(s, "@owner")
	reply, cont, err := e.Handle("user:1098392910", "new task")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !cont || !strings.Contains(reply, "Check actor lookup") {
		t.Fatalf("unexpected reply cont=%v: %s", cont, reply)
	}
	reply, cont, err = e.Handle("user:1098392910", "1 b")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if cont || !strings.Contains(reply, "Assigned") {
		t.Fatalf("unexpected pick reply cont=%v: %s", cont, reply)
	}
	if active, _ := s.ActiveTasksFor(staff.ID); len(active) != 1 || active[0].Title != "Check actor lookup" {
		t.Fatalf("task was not assigned: %+v", active)
	}
}

func TestEngineReportsExpiredNumericSelection(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	reply, cont, err := NewEngine(store.New(d), "@owner").Handle("user:1098392910", "1 b")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if cont || !strings.Contains(reply, "don't have an active task selection") {
		t.Fatalf("unexpected reply cont=%v: %s", cont, reply)
	}
}
