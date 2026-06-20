package main

import (
	"path/filepath"
	"testing"

	"github.com/Zouriel/zcoms-team/internal/db"
	"github.com/Zouriel/zcoms-team/internal/store"
)

func TestStandupTargetPrefersTelegramUserID(t *testing.T) {
	st := &store.Staff{TelegramUsername: "@ahm3dyaseen", TelegramUserID: 1098392910}
	if got := standupTarget(st); got != "1098392910" {
		t.Fatalf("target = %q", got)
	}
	st.TelegramUserID = 0
	if got := standupTarget(st); got != "@ahm3dyaseen" {
		t.Fatalf("fallback target = %q", got)
	}
}

func TestStandupDoneAnswerCompletesTask(t *testing.T) {
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
	standup, err := s.CreateStandup("hems-daily", delegator.ID, "@hems_team", "09:00", "Indian/Maldives", "@owner")
	if err != nil {
		t.Fatalf("standup: %v", err)
	}
	staff, err := s.AddStaff(delegator.ID, "@ali", "ali", store.RoleStaff, 2, "@owner")
	if err != nil {
		t.Fatalf("staff: %v", err)
	}
	task, err := s.AddTask(delegator.ID, "Finish the dashboard", store.PriorityHigh, "@owner")
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if _, err := s.AssignTask(task.ID, staff.ID, "@ali"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	run, err := s.CreateStandupRun(standup.ID, "2026-06-20")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	co := NewCoordinator(NewEngine(s, "@owner"), nil)
	co.runs[run.ID] = &runState{run: run, standup: standup, expected: 2}
	co.OnResult(InterviewResult{
		RunID:   run.ID,
		StaffID: staff.ID,
		Answers: []InterviewAnswer{{
			TaskID:   task.ID,
			Title:    task.Title,
			Response: "Done, completed today",
		}},
	})

	active, err := s.ActiveTasksFor(staff.ID)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("done standup task still active: %v", active)
	}
	updated, err := s.TaskByID(task.ID)
	if err != nil {
		t.Fatalf("task lookup: %v", err)
	}
	if updated.Status != store.StatusDone {
		t.Fatalf("task status = %q, want done", updated.Status)
	}
}
