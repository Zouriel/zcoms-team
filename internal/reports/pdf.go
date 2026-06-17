// Package reports renders detailed team reports as PDF files (pure-Go, via
// go-pdf/fpdf — no cgo).
package reports

import (
	"fmt"
	"time"

	"github.com/go-pdf/fpdf"
)

type StaffStat struct {
	Handle     string
	GithubUser string
	Role       string
	Active     int
	Completed  int
	Blocked    int
}

type TaskLine struct {
	Title    string
	Priority string
	Status   string // human label
	Assignee string
	Note     string
}

type Stats struct {
	Completed, Active, Blocked, Unassigned, Standups int
}

// Data is everything a report PDF renders.
type Data struct {
	Title       string // "Daily Report" / "Weekly Report" / "Monthly Report"
	Delegator   string
	GithubInfo  string
	Period      string // e.g. "2026-06-17" or "1–7 Jun 2026"
	GeneratedAt time.Time
	Stats       Stats
	Staff       []StaffStat
	Completed   []TaskLine
	Active      []TaskLine
	Blocked     []TaskLine
	Backlog     []TaskLine
	StandupNote string // optional: the day's standup summary
}

var (
	ink    = []int{31, 41, 55}    // slate-800
	muted  = []int{107, 114, 128} // gray-500
	accent = []int{37, 99, 235}   // blue-600
	hdrBg  = []int{243, 244, 246} // gray-100
)

// RenderPDF writes the report to outPath.
func RenderPDF(d Data, outPath string) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(16, 16, 16)
	pdf.SetAutoPageBreak(true, 16)
	pdf.AddPage()

	// Title block.
	pdf.SetTextColor(ink[0], ink[1], ink[2])
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 10, d.Title, "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.SetTextColor(muted[0], muted[1], muted[2])
	pdf.CellFormat(0, 6, fmt.Sprintf("%s   ·   %s", d.Delegator, d.GithubInfo), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 6, fmt.Sprintf("Period: %s   ·   Generated %s", d.Period, d.GeneratedAt.Format("2006-01-02 15:04 MST")), "", 1, "L", false, 0, "")
	pdf.Ln(3)
	rule(pdf)
	pdf.Ln(3)

	// Summary cards (as a simple stat row).
	sectionTitle(pdf, "Summary")
	pdf.SetFont("Helvetica", "", 11)
	pdf.SetTextColor(ink[0], ink[1], ink[2])
	stat := func(label string, n int) {
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(28, 6, fmt.Sprintf("%d", n), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 9)
		pdf.SetTextColor(muted[0], muted[1], muted[2])
		pdf.CellFormat(0, 6, label, "", 1, "L", false, 0, "")
		pdf.SetTextColor(ink[0], ink[1], ink[2])
	}
	stat("Completed (this period)", d.Stats.Completed)
	stat("Active", d.Stats.Active)
	stat("Blocked", d.Stats.Blocked)
	stat("Unassigned backlog", d.Stats.Unassigned)
	stat("Standups held", d.Stats.Standups)
	pdf.Ln(2)

	// Per-staff table.
	if len(d.Staff) > 0 {
		sectionTitle(pdf, "By staff")
		headers := []string{"Member", "Role", "Active", "Done", "Blocked"}
		widths := []float64{70, 30, 24, 24, 26}
		tableHeader(pdf, headers, widths)
		pdf.SetFont("Helvetica", "", 10)
		for _, s := range d.Staff {
			row(pdf, []string{s.Handle, s.Role, itoa(s.Active), itoa(s.Completed), itoa(s.Blocked)}, widths)
		}
		pdf.Ln(2)
	}

	taskSection(pdf, "Completed this period", d.Completed, false)
	taskSection(pdf, "In progress", d.Active, false)
	taskSection(pdf, "Blocked", d.Blocked, true)
	taskSection(pdf, "Unassigned backlog", d.Backlog, false)

	if d.StandupNote != "" {
		sectionTitle(pdf, "Standup notes")
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(ink[0], ink[1], ink[2])
		pdf.MultiCell(0, 5, d.StandupNote, "", "L", false)
	}

	return pdf.OutputFileAndClose(outPath)
}

func rule(pdf *fpdf.Fpdf) {
	pdf.SetDrawColor(229, 231, 235)
	y := pdf.GetY()
	pdf.Line(16, y, 194, y)
}

func sectionTitle(pdf *fpdf.Fpdf, t string) {
	pdf.Ln(2)
	pdf.SetFont("Helvetica", "B", 13)
	pdf.SetTextColor(accent[0], accent[1], accent[2])
	pdf.CellFormat(0, 7, t, "", 1, "L", false, 0, "")
	pdf.SetTextColor(ink[0], ink[1], ink[2])
}

func tableHeader(pdf *fpdf.Fpdf, headers []string, widths []float64) {
	pdf.SetFillColor(hdrBg[0], hdrBg[1], hdrBg[2])
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetTextColor(muted[0], muted[1], muted[2])
	for i, h := range headers {
		pdf.CellFormat(widths[i], 7, h, "", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetTextColor(ink[0], ink[1], ink[2])
}

func row(pdf *fpdf.Fpdf, cells []string, widths []float64) {
	for i, c := range cells {
		pdf.CellFormat(widths[i], 6, truncate(c, int(widths[i]*0.55)), "", 0, "L", false, 0, "")
	}
	pdf.Ln(-1)
}

func taskSection(pdf *fpdf.Fpdf, title string, lines []TaskLine, showNote bool) {
	if len(lines) == 0 {
		return
	}
	sectionTitle(pdf, fmt.Sprintf("%s (%d)", title, len(lines)))
	pdf.SetFont("Helvetica", "", 10)
	for _, l := range lines {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(ink[0], ink[1], ink[2])
		bullet := "•  " + l.Title
		pdf.MultiCell(0, 5, bullet, "", "L", false)
		meta := ""
		if l.Priority != "" {
			meta += "priority: " + l.Priority + "   "
		}
		if l.Status != "" {
			meta += "status: " + l.Status + "   "
		}
		if l.Assignee != "" {
			meta += "owner: " + l.Assignee
		}
		if meta != "" {
			pdf.SetFont("Helvetica", "", 9)
			pdf.SetTextColor(muted[0], muted[1], muted[2])
			pdf.MultiCell(0, 4.5, "   "+meta, "", "L", false)
		}
		if showNote && l.Note != "" {
			pdf.SetFont("Helvetica", "I", 9)
			pdf.SetTextColor(muted[0], muted[1], muted[2])
			pdf.MultiCell(0, 4.5, "   ↳ "+l.Note, "", "L", false)
		}
	}
	pdf.Ln(1)
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func truncate(s string, max int) string {
	if max < 4 {
		max = 4
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
