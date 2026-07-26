package register

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
)

const (
	exampleVehicles = "../../examples/vehicles.csv"
	exampleDrivers  = "../../examples/drivers.csv"
	examplePlates   = "../../examples/plates.csv"
)

func TestLoadVehiclesExample(t *testing.T) {
	res, err := LoadVehicles(exampleVehicles)
	if err != nil {
		t.Fatalf("LoadVehicles: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("shipped example produced warnings: %v", res.Warnings)
	}
	if got, want := len(res.Register.Vehicles), 8; got != want {
		t.Fatalf("vehicles = %d, want %d", got, want)
	}

	v := res.Register.Vehicles[0]
	if v.Plate != "AB12CDE" || v.Display != "AB12 CDE" {
		t.Errorf("first vehicle = %q/%q, want AB12CDE/AB12 CDE", v.Plate, v.Display)
	}
	if len(v.Items) != len(VehicleColumns) {
		t.Errorf("items = %d, want one per dated column (%d)", len(v.Items), len(VehicleColumns))
	}
	mot := findItem(t, v.Items, fleet.KindMOT)
	if want := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC); !mot.Expires.Equal(want) {
		t.Errorf("MOT expiry = %v, want %v", mot.Expires, want)
	}
	if mot.Source != fleet.SourceManual {
		t.Errorf("MOT source = %q, want manual until an API pull replaces it", mot.Source)
	}
	// AB12 CDE has neither an ADR certificate nor a LOLER date in the example.
	adr := findItem(t, v.Items, fleet.KindADR)
	if adr.Recorded() {
		t.Errorf("ADR should be unrecorded, got %v", adr.Expires)
	}
}

func TestLoadVehiclesFlagsBadRowsWithoutLosingGoodOnes(t *testing.T) {
	res, err := LoadVehicles("testdata/messy.csv")
	if err != nil {
		t.Fatalf("LoadVehicles: %v", err)
	}
	if got, want := len(res.Register.Vehicles), 3; got != want {
		t.Fatalf("vehicles = %d, want %d (AB12CDE, CF67NPR, DG21RST)", got, want)
	}

	var got []string
	for _, w := range res.Warnings {
		got = append(got, w.Error())
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"duplicate registration",                // ab12 cde repeated with a space
		"Northern Ireland",                      // GAZ 1234 is out of scope
		`"12/08/2026" is not a 2006-01-02 date`, // British-style date
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q:\n%s", want, joined)
		}
	}

	// The row with the bad MOT date is still imported, minus that one date.
	cf := res.Register.Vehicles[1]
	if cf.Plate != "CF67NPR" {
		t.Fatalf("second vehicle = %q, want CF67NPR", cf.Plate)
	}
	if ins := findItem(t, cf.Items, fleet.KindInsurance); !ins.Recorded() {
		t.Error("insurance date was dropped along with the bad MOT date")
	}
	for _, it := range cf.Items {
		if it.Kind == fleet.KindMOT {
			t.Error("an unparseable MOT date was imported anyway")
		}
	}
}

func TestLoadDriversExample(t *testing.T) {
	res, err := LoadDrivers(exampleDrivers)
	if err != nil {
		t.Fatalf("LoadDrivers: %v", err)
	}
	if got, want := len(res.Register.Drivers), 4; got != want {
		t.Fatalf("drivers = %d, want %d", got, want)
	}
	d := res.Register.Drivers[1]
	if d.ID != "D02" || d.Name != "S. Kaur" {
		t.Fatalf("second driver = %q/%q", d.ID, d.Name)
	}
	med := findItem(t, d.Items, fleet.KindDriverMedical)
	if med.Recorded() {
		t.Error("S. Kaur has no medical date in the example; it must stay unrecorded")
	}
	if med.SubjectType != fleet.SubjectDriver {
		t.Errorf("subject type = %q, want driver", med.SubjectType)
	}
}

func TestLoadPlatesSkipsHeaderAndDeduplicates(t *testing.T) {
	res, err := LoadPlates(examplePlates)
	if err != nil {
		t.Fatalf("LoadPlates: %v", err)
	}
	if got, want := len(res.Register.Vehicles), 8; got != want {
		t.Fatalf("vehicles = %d, want %d (the 'registration' header must not count)", got, want)
	}
	if res.Register.Vehicles[0].Display != "AB12 CDE" {
		t.Errorf("first plate = %q, want AB12 CDE", res.Register.Vehicles[0].Display)
	}
	for _, v := range res.Register.Vehicles {
		// A plate list carries no dates, but it must still list the two items
		// an API pull will fill in, so the operator sees the gap.
		if len(v.Items) != 2 {
			t.Errorf("%s: items = %d, want a pending MOT and tax row", v.Plate, len(v.Items))
		}
		for _, it := range v.Items {
			if it.Recorded() {
				t.Errorf("%s: %s has a date it cannot possibly know", v.Plate, it.Kind)
			}
			if it.Source != fleet.SourcePending {
				t.Errorf("%s: %s source = %q, want pending", v.Plate, it.Kind, it.Source)
			}
		}
	}
}

func TestItemsSortedByExpiryWithUnrecordedLast(t *testing.T) {
	res, err := LoadVehicles(exampleVehicles)
	if err != nil {
		t.Fatalf("LoadVehicles: %v", err)
	}
	items := res.Register.Items()
	seenUnrecorded := false
	var prev time.Time
	for _, it := range items {
		if !it.Recorded() {
			seenUnrecorded = true
			continue
		}
		if seenUnrecorded {
			t.Fatal("a dated item sorted after an undated one")
		}
		if !prev.IsZero() && it.Expires.Before(prev) {
			t.Fatalf("out of order: %v before %v", it.Expires, prev)
		}
		prev = it.Expires
	}
	if !seenUnrecorded {
		t.Fatal("expected the example to contain at least one undated item")
	}
}

func TestWriteVehiclesRoundTrip(t *testing.T) {
	res, err := LoadVehicles(exampleVehicles)
	if err != nil {
		t.Fatalf("LoadVehicles: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteVehicles(&buf, res.Register); err != nil {
		t.Fatalf("WriteVehicles: %v", err)
	}

	path := t.TempDir() + "/vehicles.csv"
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := LoadVehicles(path)
	if err != nil {
		t.Fatalf("re-reading written file: %v", err)
	}
	if len(again.Warnings) != 0 {
		t.Fatalf("written file did not read back cleanly: %v", again.Warnings)
	}
	if len(again.Register.Vehicles) != len(res.Register.Vehicles) {
		t.Fatalf("round trip lost vehicles: %d -> %d",
			len(res.Register.Vehicles), len(again.Register.Vehicles))
	}
	for i, before := range res.Register.Vehicles {
		after := again.Register.Vehicles[i]
		if before.Plate != after.Plate || before.Make != after.Make {
			t.Errorf("vehicle %d changed: %+v -> %+v", i, before, after)
		}
		for _, c := range VehicleColumns {
			b := findItem(t, before.Items, c.kind)
			a := findItem(t, after.Items, c.kind)
			if !b.Expires.Equal(a.Expires) {
				t.Errorf("%s %s: %v -> %v", before.Plate, c.kind, b.Expires, a.Expires)
			}
		}
	}
}

func TestSetItemReplacesSameKind(t *testing.T) {
	v := fleet.Vehicle{Plate: "AB12CDE"}
	SetItem(&v, fleet.Item{Kind: fleet.KindMOT, Source: fleet.SourceManual})
	SetItem(&v, fleet.Item{Kind: fleet.KindMOT, Source: fleet.SourceDVSA})
	SetItem(&v, fleet.Item{Kind: fleet.KindTax, Source: fleet.SourceDVLA})
	if len(v.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(v.Items))
	}
	if v.Items[0].Source != fleet.SourceDVSA {
		t.Errorf("MOT source = %q, want the DVSA value to have replaced the manual one", v.Items[0].Source)
	}
}

func findItem(t *testing.T, items []fleet.Item, kind fleet.Kind) fleet.Item {
	t.Helper()
	for _, it := range items {
		if it.Kind == kind {
			return it
		}
	}
	return fleet.Item{}
}
