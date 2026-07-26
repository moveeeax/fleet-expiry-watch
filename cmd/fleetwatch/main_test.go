package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	vehicles = "../../examples/vehicles.csv"
	drivers  = "../../examples/drivers.csv"
	// The examples are dated so that this reference day exercises every rung
	// of the ladder. Pinning it keeps the README example reproducible.
	refDay = "2026-07-01"
)

func exec(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	err = run(args, &out, &errBuf)
	return out.String(), errBuf.String(), err
}

func TestReportTableOnTheExampleFleet(t *testing.T) {
	out, errOut, err := exec(t, "report",
		"-vehicles", vehicles, "-drivers", drivers,
		"-operator", "Pennine Haulage Ltd", "-today", refDay)
	if err != nil {
		t.Fatalf("report failed: %v\nstderr: %s", err, errOut)
	}
	if errOut != "" {
		t.Errorf("shipped examples produced warnings: %s", errOut)
	}
	for _, want := range []string{
		"Pennine Haulage Ltd - expiry register as at 2026-07-01",
		"Contains public sector information licensed under the Open Government Licence v3.0.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
	// The three things that were already out of date on 1 July 2026.
	assertRow(t, out, "LAPSED", "AB12 CDE", "MOT")       // ran out on 12 June
	assertRow(t, out, "LAPSED", "GK15 ABC", "Insurance") // ran out on 30 June
	assertRow(t, out, "LAPSED", "S. Kaur", "Driver CPC") // ran out on 20 June
	assertRow(t, out, "OK", "HL22 DEF", "MOT")           // not due until 2027
	if !strings.Contains(out, "3 lapsed,") {
		t.Errorf("expected exactly 3 lapsed items on %s:\n%s", refDay, out)
	}
}

func TestReportFailOnLapsedExitsNonZero(t *testing.T) {
	_, _, err := exec(t, "report", "-vehicles", vehicles, "-today", refDay, "-fail-on-lapsed")
	var ec exitCode
	if !errors.As(err, &ec) || int(ec) != 3 {
		t.Fatalf("error = %v, want exit code 3", err)
	}

	// A day before anything lapses, the same command succeeds.
	_, _, err = exec(t, "report", "-vehicles", vehicles, "-today", "2026-06-01", "-fail-on-lapsed")
	if err != nil {
		t.Fatalf("clean fleet still failed: %v", err)
	}
}

func TestReportHTMLToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.html")
	if _, _, err := exec(t, "report", "-vehicles", vehicles, "-drivers", drivers,
		"-today", refDay, "-format", "html", "-out", path); err != nil {
		t.Fatalf("report -format html: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if !strings.HasPrefix(page, "<!doctype html>") {
		t.Errorf("status page does not start with a doctype: %.40s", page)
	}
	if !strings.Contains(page, "AB12 CDE") || !strings.Contains(page, "S. Kaur") {
		t.Error("status page is missing vehicles or drivers")
	}
}

func TestReportRejectsUnknownFormat(t *testing.T) {
	_, _, err := exec(t, "report", "-vehicles", vehicles, "-today", refDay, "-format", "pdf")
	if err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("error = %v, want an unknown-format error", err)
	}
}

func TestDigestWindow(t *testing.T) {
	out, _, err := exec(t, "digest", "-vehicles", vehicles, "-drivers", drivers,
		"-today", refDay, "-window", "30")
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if !strings.Contains(out, "compliance digest, Wednesday 1 July 2026") {
		t.Errorf("digest heading is wrong:\n%s", out)
	}
	if !strings.Contains(out, "LAPSED - act today (3)") {
		t.Errorf("digest lapsed count is wrong:\n%s", out)
	}
	// HL22 DEF's LOLER inspection falls due on 2 July, inside the window.
	if !strings.Contains(out, "LOLER inspection") {
		t.Errorf("digest is missing an item due inside the window:\n%s", out)
	}
	// DG21 RST's MOT is 60 days out, outside a 30-day window.
	if strings.Contains(out, "DG21 RST") {
		t.Errorf("an item outside the window leaked into the digest:\n%s", out)
	}
}

func TestAlertsOnlyFireOnLadderDays(t *testing.T) {
	// 1 July 2026 is T-14 for GK15 ABC's MOT and T-1 for BD19 KLM's MOT.
	out, _, err := exec(t, "alerts", "-vehicles", vehicles, "-today", refDay)
	if err != nil {
		t.Fatalf("alerts: %v", err)
	}
	for _, want := range []string{"GK15 ABC", "BD19 KLM"} {
		if !strings.Contains(out, want) {
			t.Errorf("alerts missing %q:\n%s", want, out)
		}
	}
	// EH70 UVW's tacho calibration is 30 days out on 1 July, so it fires too,
	// but its MOT is 192 days out and must stay quiet.
	if strings.Contains(out, "EH70 UVW  MOT") {
		t.Errorf("an item far from any threshold was alerted:\n%s", out)
	}

	// A day with no thresholds and nothing lapsed produces nothing.
	quiet, _, err := exec(t, "alerts", "-vehicles", vehicles, "-today", "2026-05-20")
	if err != nil {
		t.Fatalf("alerts: %v", err)
	}
	if !strings.Contains(quiet, "no escalation due on 2026-05-20") {
		t.Errorf("expected a quiet day, got:\n%s", quiet)
	}
}

func TestPlatesOnlyInputIsUsable(t *testing.T) {
	out, _, err := exec(t, "report", "-plates", "../../examples/plates.csv", "-today", refDay)
	if err != nil {
		t.Fatalf("report from a plate list: %v", err)
	}
	assertRow(t, out, "UNKNOWN", "AB12 CDE", "MOT")
	if !strings.Contains(out, "pending") {
		t.Errorf("plate list rows are not marked as awaiting an API pull:\n%s", out)
	}
	if !strings.Contains(out, "0 lapsed, 0 due within 60 days, 0 ok, 16 with no date recorded") {
		t.Errorf("a bare plate list has no dates yet:\n%s", out)
	}
}

func TestNoInputIsAnError(t *testing.T) {
	_, _, err := exec(t, "report", "-today", refDay)
	if err == nil || !strings.Contains(err.Error(), "nothing to read") {
		t.Fatalf("error = %v, want a 'nothing to read' error", err)
	}
}

func TestBadTodayIsAnError(t *testing.T) {
	_, _, err := exec(t, "report", "-vehicles", vehicles, "-today", "01/07/2026")
	if err == nil || !strings.Contains(err.Error(), "-today") {
		t.Fatalf("error = %v, want a date-format error", err)
	}
}

func TestFetchRefusesWithoutCredentials(t *testing.T) {
	for _, k := range []string{
		"FLEETWATCH_DVSA_CLIENT_ID", "FLEETWATCH_DVSA_CLIENT_SECRET",
		"FLEETWATCH_DVSA_TOKEN_URL", "FLEETWATCH_DVSA_API_KEY",
		"FLEETWATCH_DVLA_API_KEY",
	} {
		t.Setenv(k, "")
	}
	out := filepath.Join(t.TempDir(), "vehicles.csv")
	_, _, err := exec(t, "fetch", "-plates", "../../examples/plates.csv", "-out", out)
	if err == nil {
		t.Fatal("fetch succeeded with no credentials configured")
	}
	if !strings.Contains(err.Error(), "no data source is configured") {
		t.Errorf("error = %v, want a clear 'no data source' message", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("fetch wrote a file despite having nothing to fetch")
	}
}

func TestFetchRequiresOut(t *testing.T) {
	_, _, err := exec(t, "fetch", "-plates", "../../examples/plates.csv")
	if err == nil || !strings.Contains(err.Error(), "-out is required") {
		t.Fatalf("error = %v, want an -out required error", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	_, stderr, err := exec(t, "audit")
	if err == nil || !strings.Contains(err.Error(), `unknown command "audit"`) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Error("an unknown command should print the usage")
	}
}

func TestVersion(t *testing.T) {
	out, _, err := exec(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(out, "fleetwatch ") {
		t.Errorf("version output = %q", out)
	}
}

// assertRow checks that one line of the table carries all three fields, without
// pinning the column widths, which depend on the widest value in the fleet.
func assertRow(t *testing.T, out, status, subject, item string) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, status+" ") &&
			strings.Contains(line, subject) && strings.Contains(line, item) {
			return
		}
	}
	t.Errorf("no %s row for %s / %s:\n%s", status, subject, item, out)
}
