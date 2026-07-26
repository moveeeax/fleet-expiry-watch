// Package report renders assessed items as a terminal table, a CSV export or a
// printable single-page fleet status sheet.
package report

import (
	"encoding/csv"
	"fmt"
	"html/template"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/expiry"
)

// OGLAttribution is the acknowledgement the Open Government Licence v3.0
// requires wherever DVSA/DVLA data is displayed. It goes on every export.
const OGLAttribution = "Contains public sector information licensed under the Open Government Licence v3.0."

// Options carries the rendering context shared by all formats.
type Options struct {
	// Operator is the licence holder's name, printed as the report heading.
	Operator string
	// Today is the day the assessment was made against.
	Today time.Time
}

// Name is the heading to print, falling back to a neutral label.
func (o Options) Name() string {
	if strings.TrimSpace(o.Operator) == "" {
		return "Fleet"
	}
	return o.Operator
}

// Counts summarises a set of assessments by traffic-light level.
type Counts struct {
	Lapsed  int
	Due     int
	OK      int
	Unknown int
}

// Tally counts assessments by level.
func Tally(as []expiry.Assessment) Counts {
	var c Counts
	for _, a := range as {
		switch a.Level {
		case expiry.LevelRed:
			c.Lapsed++
		case expiry.LevelAmber:
			c.Due++
		case expiry.LevelGreen:
			c.OK++
		default:
			c.Unknown++
		}
	}
	return c
}

// subjectLabel is the display name of the vehicle or driver the item hangs off.
func subjectLabel(a expiry.Assessment) string { return a.Item.SubjectName }

func daysCell(a expiry.Assessment) string {
	if a.Level == expiry.LevelUnknown {
		return "-"
	}
	return strconv.Itoa(a.Days)
}

func dateCell(a expiry.Assessment) string {
	if a.Level == expiry.LevelUnknown {
		return "not recorded"
	}
	return a.Item.Expires.Format("2006-01-02")
}

// Table writes a fixed-width traffic-light table for the terminal.
func Table(w io.Writer, as []expiry.Assessment, opt Options) error {
	fmt.Fprintf(w, "%s - expiry register as at %s\n\n", opt.Name(), opt.Today.Format("2006-01-02"))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tSUBJECT\tITEM\tEXPIRES\tDAYS\tSTAGE\tSOURCE")
	for _, a := range as {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			a.Level, subjectLabel(a), a.Item.Kind.Label(),
			dateCell(a), daysCell(a), a.Stage, a.Item.Source)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	c := Tally(as)
	fmt.Fprintf(w, "\n%d lapsed, %d due within %d days, %d ok, %d with no date recorded\n",
		c.Lapsed, c.Due, expiry.Thresholds[0], c.OK, c.Unknown)
	fmt.Fprintf(w, "\n%s\n", OGLAttribution)
	return nil
}

// CSVHeader is the export column order.
var CSVHeader = []string{
	"subject_type", "subject_id", "subject", "item", "item_kind",
	"expires", "days_remaining", "status", "stage", "source", "note",
}

// CSV writes the full export, ending with the OGL attribution row.
func CSV(w io.Writer, as []expiry.Assessment, opt Options) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(CSVHeader); err != nil {
		return err
	}
	for _, a := range as {
		rec := []string{
			string(a.Item.SubjectType),
			a.Item.SubjectID,
			a.Item.SubjectName,
			a.Item.Kind.Label(),
			string(a.Item.Kind),
			"",
			daysCell(a),
			a.Level.String(),
			string(a.Stage),
			string(a.Item.Source),
			a.Item.Note,
		}
		if a.Item.Recorded() {
			rec[5] = a.Item.Expires.Format("2006-01-02")
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	// Padded to the full width so the export stays rectangular for strict
	// CSV parsers as well as for spreadsheets.
	attribution := make([]string, len(CSVHeader))
	attribution[0] = OGLAttribution
	if err := cw.Write(attribution); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// Digest writes the Monday morning summary: what has lapsed, then what expires
// inside the window. It is deliberately plain text so it can be pasted straight
// into an email body.
func Digest(w io.Writer, as []expiry.Assessment, window int, opt Options) error {
	lapsed, due := expiry.Digest(as, window)

	fmt.Fprintf(w, "%s - compliance digest, %s\n", opt.Name(), opt.Today.Format("Monday 2 January 2006"))
	fmt.Fprintf(w, "%s\n\n", strings.Repeat("=", 60))

	fmt.Fprintf(w, "LAPSED - act today (%d)\n", len(lapsed))
	if len(lapsed) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, a := range lapsed {
		fmt.Fprintf(w, "  %-22s %-22s %s (%s)\n",
			subjectLabel(a), a.Item.Kind.Label(), a.Summary(), dateCell(a))
	}

	fmt.Fprintf(w, "\nEXPIRING WITHIN %d DAYS (%d)\n", window, len(due))
	if len(due) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for _, a := range due {
		fmt.Fprintf(w, "  %-22s %-22s %s (%s)\n",
			subjectLabel(a), a.Item.Kind.Label(), a.Summary(), dateCell(a))
	}

	if c := Tally(as); c.Unknown > 0 {
		fmt.Fprintf(w, "\n%d item(s) have no date recorded and are not being watched.\n", c.Unknown)
	}
	fmt.Fprintf(w, "\n%s\n", OGLAttribution)
	return nil
}

type htmlRow struct {
	Class   string
	Status  string
	Subject string
	Item    string
	Expires string
	Days    string
	Summary string
	Source  string
}

type htmlData struct {
	Operator    string
	Today       string
	Rows        []htmlRow
	Counts      Counts
	Window      int
	Attribution string
}

var statusClass = map[expiry.Level]string{
	expiry.LevelRed:     "red",
	expiry.LevelAmber:   "amber",
	expiry.LevelGreen:   "green",
	expiry.LevelUnknown: "grey",
}

// HTML writes the read-only, printable fleet status page. It is a single file
// with no external assets so it can be mailed or shared as-is.
func HTML(w io.Writer, as []expiry.Assessment, opt Options) error {
	data := htmlData{
		Operator:    opt.Name(),
		Today:       opt.Today.Format("2 January 2006"),
		Counts:      Tally(as),
		Window:      expiry.Thresholds[0],
		Attribution: OGLAttribution,
	}
	for _, a := range as {
		data.Rows = append(data.Rows, htmlRow{
			Class:   statusClass[a.Level],
			Status:  a.Level.String(),
			Subject: subjectLabel(a),
			Item:    a.Item.Kind.Label(),
			Expires: dateCell(a),
			Days:    daysCell(a),
			Summary: a.Summary(),
			Source:  string(a.Item.Source),
		})
	}
	return statusTemplate.Execute(w, data)
}

var statusTemplate = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en-GB">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Operator}} - fleet expiry status</title>
<style>
  :root { color-scheme: light; }
  body { font: 15px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", Arial, sans-serif;
         margin: 2rem auto; max-width: 60rem; padding: 0 1rem; color: #14171a; }
  h1 { font-size: 1.4rem; margin-bottom: 0.2rem; }
  .sub { color: #55606a; margin-top: 0; }
  .counts { display: flex; gap: 0.5rem; flex-wrap: wrap; margin: 1rem 0 1.5rem; padding: 0; list-style: none; }
  .counts li { border: 1px solid #d7dde3; border-radius: 6px; padding: 0.4rem 0.8rem; }
  .counts b { font-size: 1.2rem; display: block; }
  table { border-collapse: collapse; width: 100%; }
  th, td { text-align: left; padding: 0.45rem 0.6rem; border-bottom: 1px solid #e6eaee; }
  th { font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.04em; color: #55606a; }
  td.status { font-weight: 600; white-space: nowrap; }
  tr.red td.status { color: #a3110f; }
  tr.amber td.status { color: #8a5300; }
  tr.green td.status { color: #17683a; }
  tr.grey td.status { color: #55606a; }
  tr.red { background: #fdf1f0; }
  tr.amber { background: #fdf7ec; }
  footer { margin-top: 2rem; font-size: 0.8rem; color: #55606a; }
  @media print { body { margin: 0; max-width: none; } tr { break-inside: avoid; } }
</style>
</head>
<body>
<h1>{{.Operator}} &mdash; fleet expiry status</h1>
<p class="sub">As at {{.Today}}. Read-only snapshot.</p>
<ul class="counts">
  <li><b>{{.Counts.Lapsed}}</b>lapsed</li>
  <li><b>{{.Counts.Due}}</b>due within {{.Window}} days</li>
  <li><b>{{.Counts.OK}}</b>ok</li>
  <li><b>{{.Counts.Unknown}}</b>no date recorded</li>
</ul>
<table>
<thead><tr><th>Status</th><th>Subject</th><th>Item</th><th>Expires</th><th>Days</th><th>Detail</th><th>Source</th></tr></thead>
<tbody>
{{range .Rows}}<tr class="{{.Class}}"><td class="status">{{.Status}}</td><td>{{.Subject}}</td><td>{{.Item}}</td><td>{{.Expires}}</td><td>{{.Days}}</td><td>{{.Summary}}</td><td>{{.Source}}</td></tr>
{{end}}</tbody>
</table>
<footer>{{.Attribution}}</footer>
</body>
</html>
`))
