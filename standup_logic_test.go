package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Zouriel/zcoms-team/internal/store"
)

func TestDetectStatus(t *testing.T) {
	cases := map[string]string{
		"Backend complete. UI remaining.": store.StatusAssigned, // "complete" of a part → in progress overall? we map by keywords
		"Waiting for API review.":         store.StatusBlocked,
		"Done, ready for review.":         store.StatusDone,
		"Pushed the PR, in review":        store.StatusReview,
		"making progress on it":           store.StatusAssigned,
		"":                                "",
	}
	for resp, want := range cases {
		got, blk := detectStatus(resp)
		if got != want {
			t.Errorf("detectStatus(%q)=%q want %q", resp, got, want)
		}
		if want == store.StatusBlocked && blk == "" {
			t.Errorf("expected blocker note for %q", resp)
		}
	}
}

func TestGenerateReport(t *testing.T) {
	ups := []*store.TaskUpdate{
		{StaffMemberID: "s1", TaskTitle: "Fix scheme filter", DetectedStatus: store.StatusAssigned, StaffResponse: "Backend complete. UI remaining."},
		{StaffMemberID: "s1", TaskTitle: "Refactor eligibility query", DetectedStatus: store.StatusBlocked, StaffResponse: "Waiting for API review.", Blocker: "Waiting for API review."},
		{StaffMemberID: "s2", TaskTitle: "Pagination", DetectedStatus: store.StatusDone, StaffResponse: "Ready for review."},
	}
	rep := generateReport(ups, map[string]string{"s1": "@ali", "s2": "@zouriel"})
	for _, must := range []string{"Daily Standup Summary", "@ali", "In Progress", "Blocked", "@zouriel", "Done", "Blocked Tasks", "Waiting for API review."} {
		if !strings.Contains(rep, must) {
			t.Errorf("report missing %q\n---\n%s", must, rep)
		}
	}
}

func TestNextRun(t *testing.T) {
	from := time.Date(2026, 6, 17, 8, 0, 0, 0, time.UTC)
	// 09:00 UTC same day → today 09:00.
	n, err := nextRun("09:00", "UTC", from)
	if err != nil || n.Hour() != 9 || n.Day() != 17 {
		t.Fatalf("want today 09:00, got %v (%v)", n, err)
	}
	// 07:00 already past → tomorrow.
	n2, _ := nextRun("07:00", "UTC", from)
	if n2.Day() != 18 {
		t.Fatalf("want tomorrow, got %v", n2)
	}
	if _, err := nextRun("9am", "UTC", from); err == nil {
		t.Fatal("expected error on bad time")
	}
}
