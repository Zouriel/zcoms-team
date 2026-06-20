package store

import "testing"

func TestTaskPool(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.CreateDelegator("p", "o", 1, "@admin")
	staff, _ := s.AddStaff(d.ID, "@ali", "ali", "staff", 2, "@admin")

	// Add tasks across priorities (+ a duplicate that should be skipped).
	mk := func(title, pri string) {
		if _, err := s.AddTask(d.ID, title, pri, "@lead"); err != nil {
			t.Fatalf("add %q: %v", title, err)
		}
	}
	mk("Low one", PriorityLow)
	mk("Critical one", PriorityCritical)
	mk("High one", PriorityHigh)
	if dup, err := s.AddTask(d.ID, "critical ONE", PriorityHigh, "@lead"); err != nil || dup != nil {
		t.Fatalf("expected duplicate skip, got %v %v", dup, err)
	}

	// Ordering: Critical, High, Low.
	avail, _ := s.AvailableTasks(d.ID, 3)
	if len(avail) != 3 || avail[0].Title != "Critical one" || avail[1].Title != "High one" || avail[2].Title != "Low one" {
		t.Fatalf("bad ordering: %v", titles(avail))
	}

	// Claim two → limit reached.
	if _, err := s.AssignTask(avail[0].ID, staff.ID, "@ali"); err != nil {
		t.Fatalf("assign1: %v", err)
	}
	if _, err := s.AssignTask(avail[1].ID, staff.ID, "@ali"); err != nil {
		t.Fatalf("assign2: %v", err)
	}
	if n, _ := s.CountActiveFor(staff.ID); n != 2 {
		t.Fatalf("want 2 active, got %d", n)
	}
	// Re-claiming an assigned task fails.
	if _, err := s.AssignTask(avail[0].ID, staff.ID, "@ali"); err == nil {
		t.Fatal("expected re-claim to fail")
	}
	// Finish one → active drops.
	if _, err := s.FinishTask(avail[0].ID, "@ali"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if n, _ := s.CountActiveFor(staff.ID); n != 1 {
		t.Fatalf("want 1 active after finish, got %d", n)
	}
	act, _ := s.ActiveTasksFor(staff.ID)
	if len(act) != 1 || act[0].Title != "High one" {
		t.Fatalf("active wrong: %v", titles(act))
	}
	if _, err := s.UnassignTask(act[0].ID, staff.ID, "@ali"); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if n, _ := s.CountActiveFor(staff.ID); n != 0 {
		t.Fatalf("want 0 active after unassign, got %d", n)
	}
	avail, _ = s.AvailableTasks(d.ID, 3)
	if len(avail) != 2 || avail[0].Title != "High one" {
		t.Fatalf("unassigned task not returned to pool: %v", titles(avail))
	}
	// Resolve actor → staff.
	if rs, _ := s.StaffByTelegram("@ALI"); len(rs) != 1 || rs[0].ID != staff.ID {
		t.Fatalf("StaffByTelegram wrong")
	}
	if rs, _ := s.StaffByTelegram("Ali"); len(rs) != 1 || rs[0].ID != staff.ID {
		t.Fatalf("StaffByTelegram without @ wrong")
	}
	if st, _ := s.StaffByUser(d.ID, "@ALI"); st.ID != staff.ID {
		t.Fatalf("StaffByUser wrong")
	}
	if _, err := s.db.Exec(`UPDATE staff_members SET telegram_user_id=? WHERE id=?`, int64(1098392910), staff.ID); err != nil {
		t.Fatalf("set telegram_user_id: %v", err)
	}
	if rs, _ := s.StaffByTelegram("user:1098392910"); len(rs) != 1 || rs[0].ID != staff.ID {
		t.Fatalf("StaffByTelegram user id wrong")
	}
	if st, _ := s.StaffByUser(d.ID, "user:1098392910"); st.ID != staff.ID {
		t.Fatalf("StaffByUser user id wrong")
	}
}

func titles(ts []*Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Title
	}
	return out
}
