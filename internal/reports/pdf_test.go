package reports

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenderPDF(t *testing.T) {
	d := Data{
		Title: "Weekly Report", Delegator: "hems-dev", GithubInfo: "MoHE-HEMS/#1",
		Period: "Jun 10 – Jun 17, 2026", GeneratedAt: time.Now(),
		Stats: Stats{Completed: 3, Active: 2, Blocked: 1, Unassigned: 5, Standups: 5},
		Staff: []StaffStat{
			{Handle: "@ali", GithubUser: "ali-dev", Role: "staff", Active: 1, Completed: 2, Blocked: 1},
			{Handle: "@zouriel", GithubUser: "zouriel", Role: "lead", Active: 1, Completed: 1},
		},
		Completed: []TaskLine{{Title: "Fix scheme filter", Priority: "high", Status: "Done", Assignee: "@ali"}},
		Active:    []TaskLine{{Title: "Refactor eligibility query", Priority: "high", Status: "In Progress", Assignee: "@zouriel"}},
		Blocked:   []TaskLine{{Title: "API integration", Priority: "critical", Status: "Blocked", Assignee: "@ali", Note: "Waiting for API review."}},
		Backlog:   []TaskLine{{Title: "Add pagination", Priority: "medium", Status: "No update"}},
		StandupNote: "Daily Standup Summary\n\n@ali\n  Fix scheme filter\n  Done",
	}
	out := filepath.Join(t.TempDir(), "r.pdf")
	if err := RenderPDF(d, out); err != nil {
		t.Fatalf("render: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(b) < 800 || string(b[:5]) != "%PDF-" {
		t.Fatalf("not a valid PDF (len=%d, head=%q)", len(b), b[:min(8, len(b))])
	}
	t.Logf("rendered %d-byte PDF", len(b))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
