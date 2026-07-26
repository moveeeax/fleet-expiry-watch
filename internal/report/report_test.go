package report

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/expiry"
	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
)

var today = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

func opts() Options { return Options{Operator: "Pennine Haulage Ltd", Today: today} }

func sample() []expiry.Assessment {
	at := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	items := []fleet.Item{
		{SubjectType: fleet.SubjectVehicle, SubjectID: "AB12CDE", SubjectName: "AB12 CDE",
			Kind: fleet.KindMOT, Expires: at(2026, 6, 12), Source: fleet.SourceDVSA},
		{SubjectType: fleet.SubjectVehicle, SubjectID: "AB12CDE", SubjectName: "AB12 CDE",
			Kind: fleet.KindTax, Expires: at(2026, 8, 1), Source: fleet.SourceDVLA},
		{SubjectType: fleet.SubjectDriver, SubjectID: "D02", SubjectName: "S. Kaur",
			Kind: fleet.KindDriverCPC, Expires: at(2026, 7, 15), Source: fleet.SourceManual},
		{SubjectType: fleet.SubjectVehicle, SubjectID: "AB12CDE", SubjectName: "AB12 CDE",
			Kind: fleet.KindADR, Source: fleet.SourceManual},
	}
	return expiry.AssessAll(items, today)
}

func TestTable(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, sample(), opts()); err != nil {
		t.Fatalf("Table: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Pennine Haulage Ltd - expiry register as at 2026-07-01",
		"STATUS", "LAPSED", "AB12 CDE", "MOT", "2026-06-12",
		"Driver CPC (Code 95)", "not recorded",
		"1 lapsed, 2 due within 60 days, 0 ok, 1 with no date recorded",
		OGLAttribution,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestTableFallsBackToNeutralHeading(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, sample(), Options{Today: today}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "Fleet - expiry register") {
		t.Errorf("unnamed operator heading = %q", strings.SplitN(buf.String(), "\n", 2)[0])
	}
}

func TestCSVIsParseableAndAttributed(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(&buf, sample(), opts()); err != nil {
		t.Fatalf("CSV: %v", err)
	}
	r := csv.NewReader(bytes.NewReader(buf.Bytes()))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("export is not valid CSV: %v", err)
	}
	if got, want := len(records), len(sample())+2; got != want {
		t.Fatalf("rows = %d, want header + %d items + attribution", got, len(sample()))
	}
	if got := strings.Join(records[0], ","); got != strings.Join(CSVHeader, ",") {
		t.Errorf("header = %q", got)
	}

	first := records[1]
	if first[0] != "vehicle" || first[2] != "AB12 CDE" || first[5] != "2026-06-12" {
		t.Errorf("first row = %v", first)
	}
	if first[6] != "-19" {
		t.Errorf("days_remaining = %q, want -19", first[6])
	}
	if first[7] != "LAPSED" {
		t.Errorf("status = %q, want LAPSED", first[7])
	}
	if first[9] != "dvsa" {
		t.Errorf("source = %q, want dvsa", first[9])
	}

	last := records[len(records)-1]
	if last[0] != OGLAttribution {
		t.Errorf("last row = %v, want the OGL attribution the MOT data requires", last)
	}
}

func TestCSVLeavesUnrecordedDateBlank(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(&buf, sample(), opts()); err != nil {
		t.Fatalf("CSV: %v", err)
	}
	// The default reader enforces a constant field count, so this also proves
	// the export is rectangular.
	records, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("export is not rectangular: %v", err)
	}
	var found bool
	for _, rec := range records[1 : len(records)-1] {
		if rec[4] != string(fleet.KindADR) {
			continue
		}
		found = true
		if rec[5] != "" {
			t.Errorf("expires = %q, want blank for an item with no date", rec[5])
		}
		if rec[6] != "-" {
			t.Errorf("days_remaining = %q, want %q", rec[6], "-")
		}
		if rec[7] != "UNKNOWN" {
			t.Errorf("status = %q, want UNKNOWN", rec[7])
		}
	}
	if !found {
		t.Fatal("the undated ADR item is missing from the export")
	}
}

func TestDigestSeparatesLapsedFromDueSoon(t *testing.T) {
	var buf bytes.Buffer
	if err := Digest(&buf, sample(), 30, opts()); err != nil {
		t.Fatalf("Digest: %v", err)
	}
	out := buf.String()
	lapsedAt := strings.Index(out, "LAPSED - act today (1)")
	dueAt := strings.Index(out, "EXPIRING WITHIN 30 DAYS (1)")
	if lapsedAt < 0 || dueAt < 0 {
		t.Fatalf("digest is missing a section:\n%s", out)
	}
	if lapsedAt > dueAt {
		t.Error("what has already lapsed must come first")
	}
	if !strings.Contains(out, "lapsed 19 days ago") {
		t.Errorf("digest does not say how long the MOT has been lapsed:\n%s", out)
	}
	// The tax item is 31 days out and must not appear in a 30-day digest.
	if strings.Contains(out, "Vehicle tax") {
		t.Errorf("an item outside the window leaked into the digest:\n%s", out)
	}
	if !strings.Contains(out, "1 item(s) have no date recorded") {
		t.Errorf("digest does not warn about undated items:\n%s", out)
	}
}

func TestHTMLIsSelfContainedAndEscaped(t *testing.T) {
	as := sample()
	as[0].Item.SubjectName = `AB12 CDE <script>alert("x")</script>`
	var buf bytes.Buffer
	if err := HTML(&buf, as, Options{Operator: "Pennine & Sons", Today: today}); err != nil {
		t.Fatalf("HTML: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "<script>alert") {
		t.Error("subject name was not HTML-escaped")
	}
	if !strings.Contains(out, "Pennine &amp; Sons") {
		t.Error("operator name was not escaped")
	}
	for _, want := range []string{
		"<!doctype html>", `class="red"`, `class="grey"`, "LAPSED", OGLAttribution,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	for _, forbidden := range []string{"http://", "https://cdn", "<script"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("status page is not self-contained: found %q", forbidden)
		}
	}
}

func TestTally(t *testing.T) {
	c := Tally(sample())
	if c != (Counts{Lapsed: 1, Due: 2, OK: 0, Unknown: 1}) {
		t.Errorf("Tally = %+v", c)
	}
}
