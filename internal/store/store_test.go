package store

import (
	"path/filepath"
	"testing"

	"github.com/Zouriel/zcoms-team/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func TestDelegatorCRUD(t *testing.T) {
	s := newTestStore(t)
	d, err := s.CreateDelegator("hems-dev", "MoHE-HEMS", 1, "@admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.ID == "" || !d.IsActive {
		t.Fatalf("bad delegator: %+v", d)
	}
	if _, err := s.CreateDelegator("hems-dev", "x", 2, "@admin"); err == nil {
		t.Fatal("expected duplicate-name error")
	}
	got, err := s.DelegatorByName("hems-dev")
	if err != nil || got.GithubOwner != "MoHE-HEMS" || got.GithubProjectNumber != 1 {
		t.Fatalf("lookup: %v %+v", err, got)
	}
	list, _ := s.ListDelegators()
	if len(list) != 1 {
		t.Fatalf("want 1 delegator, got %d", len(list))
	}
	if err := s.DeleteDelegator("hems-dev", "@admin"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.DelegatorByName("hems-dev"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestStandupAndStaff(t *testing.T) {
	s := newTestStore(t)
	d, _ := s.CreateDelegator("ims-dev", "org", 2, "@admin")

	su, err := s.CreateStandup("ims-daily", d.ID, "@ims_team", "09:00", "Indian/Maldives", "@admin")
	if err != nil {
		t.Fatalf("standup: %v", err)
	}
	if sus, _ := s.ListStandups(d.ID); len(sus) != 1 || sus[0].ID != su.ID {
		t.Fatalf("standup list wrong")
	}

	st, err := s.AddStaff(d.ID, "ali", "ali-dev", "staff", 2, "@admin")
	if err != nil {
		t.Fatalf("addstaff: %v", err)
	}
	if st.TelegramUsername != "@ali" {
		t.Fatalf("username not normalized: %q", st.TelegramUsername)
	}
	if err := s.SetStaffRole(d.ID, "@ali", "lead", "@admin"); err != nil {
		t.Fatalf("role: %v", err)
	}
	if err := s.SetStaffLimit(d.ID, "@ali", 3, "@admin"); err != nil {
		t.Fatalf("limit: %v", err)
	}
	got, _ := s.StaffByUser(d.ID, "@ali")
	if got.Role != "lead" || got.MaxActiveTasks != 3 {
		t.Fatalf("update not applied: %+v", got)
	}
	if err := s.RemoveStaff(d.ID, "@ali", "@admin"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := s.StaffByUser(d.ID, "@ali"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// Re-add reactivates.
	if _, err := s.AddStaff(d.ID, "@ali", "ali-dev", "staff", 2, "@admin"); err != nil {
		t.Fatalf("readd: %v", err)
	}
	if list, _ := s.ListStaff(d.ID); len(list) != 1 {
		t.Fatalf("want 1 active staff, got %d", len(list))
	}

	// Audit trail should have entries.
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&n)
	if n < 5 {
		t.Fatalf("expected audit entries, got %d", n)
	}
}
