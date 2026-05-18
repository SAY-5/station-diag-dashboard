package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/store"
)

// reportData bundles everything needed to render a run report.
type reportData struct {
	Run      store.Run
	Events   []store.RunEvent
	Failures []failureRow
	Notes    []store.Note
}

type failureRow struct {
	RuleID   string
	Actuator string
	Detail   string
	Severity string
	At       time.Time
}

// renderMarkdown produces an operator-facing Markdown report for one run:
// the run summary, the failure list, the event timeline and operator notes.
func renderMarkdown(d reportData) string {
	var b strings.Builder
	r := d.Run

	fmt.Fprintf(&b, "# Run report: %s\n\n", r.RunID)
	fmt.Fprintf(&b, "- Station: `%s`\n", r.StationID)
	fmt.Fprintf(&b, "- First seen: %s\n", r.FirstSeen.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Last seen: %s\n", r.LastSeen.Format(time.RFC3339))
	fmt.Fprintf(&b, "- Events: %d\n", r.EventCount)
	fmt.Fprintf(&b, "- Failures flagged: %d\n", len(d.Failures))
	status := "open"
	if r.Resolved {
		status = "resolved"
	}
	fmt.Fprintf(&b, "- Status: %s\n\n", status)

	b.WriteString("## Flagged failures\n\n")
	if len(d.Failures) == 0 {
		b.WriteString("No actuator failures were flagged for this run.\n\n")
	} else {
		b.WriteString("| Rule | Actuator | Severity | When | Detail |\n")
		b.WriteString("|------|----------|----------|------|--------|\n")
		for _, f := range d.Failures {
			act := f.Actuator
			if act == "" {
				act = "n/a"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				f.RuleID, act, f.Severity,
				f.At.Format(time.RFC3339), escapeCell(f.Detail))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Event timeline\n\n")
	if len(d.Events) == 0 {
		b.WriteString("No events recorded.\n\n")
	} else {
		for _, e := range d.Events {
			act := ""
			if e.Actuator != "" {
				act = " [" + e.Actuator + "]"
			}
			fmt.Fprintf(&b, "- `%s` **%s** %s/%s%s: %s\n",
				e.TS.Format("15:04:05.000"), strings.ToUpper(e.Level),
				e.Subsystem, e.Level, act, e.Message)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Operator notes\n\n")
	if len(d.Notes) == 0 {
		b.WriteString("No operator notes were attached to this run.\n\n")
	} else {
		for _, n := range d.Notes {
			author := n.Author
			if author == "" {
				author = "anonymous"
			}
			fmt.Fprintf(&b, "- **%s** at %s: %s\n",
				author, n.CreatedAt.Format(time.RFC3339), n.Body)
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n")
	fmt.Fprintf(&b, "Report generated %s by station-diag-dashboard.\n",
		time.Now().UTC().Format(time.RFC3339))
	return b.String()
}

// escapeCell keeps a detail string safe inside a Markdown table cell.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}
