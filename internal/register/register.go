// Package register reads the operator's fleet out of CSV files.
//
// Three shapes are accepted:
//
//	plates.csv    a single column of registration marks (the fastest way in)
//	vehicles.csv  one row per vehicle with its dated items in columns
//	drivers.csv   one row per driver with their dated items in columns
//
// Column order does not matter; the header names do. Unknown columns are
// ignored so an operator can paste in their own spreadsheet without stripping
// it down first.
package register

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
	"github.com/moveeeax/fleet-expiry-watch/internal/plate"
)

// DateLayout is the only date format accepted in CSV input. ISO 8601 avoids the
// permanent DD/MM versus MM/DD ambiguity in fleet spreadsheets.
const DateLayout = "2006-01-02"

// Column binds a CSV header to an item kind. Kept as an ordered slice so that
// output is byte-for-byte reproducible.
type Column struct {
	name string
	kind fleet.Kind
}

// VehicleColumns are the dated columns of vehicles.csv, in report order.
var VehicleColumns = []Column{
	{"mot_expiry", fleet.KindMOT},
	{"tax_due", fleet.KindTax},
	{"insurance_expiry", fleet.KindInsurance},
	{"olicence_expiry", fleet.KindOperatorLicence},
	{"tacho_calibration_due", fleet.KindTachoCalibration},
	{"adr_expiry", fleet.KindADR},
	{"loler_due", fleet.KindLOLER},
}

// DriverColumns are the dated columns of drivers.csv, in report order.
var DriverColumns = []Column{
	{"licence_expiry", fleet.KindDriverLicence},
	{"cpc_expiry", fleet.KindDriverCPC},
	{"tacho_card_expiry", fleet.KindTachoCard},
	{"medical_expiry", fleet.KindDriverMedical},
}

// apiBacked lists the kinds a nightly pull can fill in for itself. Dates for
// these are marked as coming from the API when they are not in the CSV.
var apiBacked = map[fleet.Kind]bool{fleet.KindMOT: true, fleet.KindTax: true}

// Warning is a non-fatal problem with one input row. Bad rows are reported and
// skipped: one mistyped plate must not stop a 50-vehicle import.
type Warning struct {
	File string
	Line int
	Err  error
}

func (w Warning) Error() string { return fmt.Sprintf("%s:%d: %v", w.File, w.Line, w.Err) }

// Result is a parsed register plus everything that was skipped.
type Result struct {
	Register fleet.Register
	Warnings []Warning
}

type table struct {
	name    string
	header  map[string]int
	records [][]string
}

func readTable(name string, r io.Reader) (*table, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%s: file is empty", name)
	}
	header := map[string]int{}
	for i, h := range records[0] {
		header[normaliseHeader(h)] = i
	}
	return &table{name: name, header: header, records: records[1:]}, nil
}

func normaliseHeader(h string) string {
	h = strings.TrimPrefix(h, "\ufeff") // Excel writes a BOM on the first cell
	h = strings.ToLower(strings.TrimSpace(h))
	return strings.ReplaceAll(h, " ", "_")
}

func (t *table) cell(row []string, name string) string {
	i, ok := t.header[name]
	if !ok || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// LoadPlates reads a single-column list of registration marks. A header row is
// optional: a first row that does not parse as a plate is treated as one.
func LoadPlates(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	records, err := cr.ReadAll()
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}

	var res Result
	seen := map[string]bool{}
	for i, rec := range records {
		if len(rec) == 0 || strings.TrimSpace(rec[0]) == "" {
			continue
		}
		mark, err := plate.Parse(rec[0])
		if err != nil {
			if i == 0 {
				continue // header row
			}
			res.Warnings = append(res.Warnings, Warning{File: path, Line: i + 1, Err: err})
			continue
		}
		if seen[mark.Normalised] {
			continue
		}
		seen[mark.Normalised] = true
		v := fleet.Vehicle{Plate: mark.Normalised, Display: mark.String()}
		// A bare plate list still produces a row per API-backed item, so the
		// operator sees the vehicle and the gap on the very first run.
		for kind := range apiBacked {
			v.Items = append(v.Items, pendingItem(v.Plate, v.Display, kind))
		}
		sort.Slice(v.Items, func(a, b int) bool { return v.Items[a].Kind < v.Items[b].Kind })
		res.Register.Vehicles = append(res.Register.Vehicles, v)
	}
	return res, nil
}

// LoadVehicles reads vehicles.csv.
func LoadVehicles(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	t, err := readTable(path, f)
	if err != nil {
		return Result{}, err
	}
	if _, ok := t.header["plate"]; !ok {
		return Result{}, fmt.Errorf("%s: missing required column %q", path, "plate")
	}

	var res Result
	seen := map[string]bool{}
	for i, row := range t.records {
		line := i + 2
		raw := t.cell(row, "plate")
		if raw == "" {
			continue
		}
		mark, err := plate.Parse(raw)
		if err != nil {
			res.Warnings = append(res.Warnings, Warning{File: path, Line: line, Err: err})
			continue
		}
		if seen[mark.Normalised] {
			res.Warnings = append(res.Warnings, Warning{File: path, Line: line,
				Err: fmt.Errorf("%s: duplicate registration, later row ignored", mark)})
			continue
		}
		seen[mark.Normalised] = true

		v := fleet.Vehicle{
			Plate:       mark.Normalised,
			Display:     mark.String(),
			FleetNumber: t.cell(row, "fleet_number"),
			Make:        t.cell(row, "make"),
			Model:       t.cell(row, "model"),
		}
		for _, c := range VehicleColumns {
			item, warn := t.item(row, c.name, c.kind, line, fleet.SubjectVehicle, v.Plate, v.Display)
			if warn != nil {
				res.Warnings = append(res.Warnings, *warn)
				continue
			}
			v.Items = append(v.Items, item)
		}
		res.Register.Vehicles = append(res.Register.Vehicles, v)
	}
	return res, nil
}

// LoadDrivers reads drivers.csv.
func LoadDrivers(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	t, err := readTable(path, f)
	if err != nil {
		return Result{}, err
	}
	if _, ok := t.header["name"]; !ok {
		return Result{}, fmt.Errorf("%s: missing required column %q", path, "name")
	}

	var res Result
	for i, row := range t.records {
		line := i + 2
		name := t.cell(row, "name")
		if name == "" {
			continue
		}
		id := t.cell(row, "driver_id")
		if id == "" {
			id = name
		}
		d := fleet.Driver{ID: id, Name: name}
		for _, c := range DriverColumns {
			item, warn := t.item(row, c.name, c.kind, line, fleet.SubjectDriver, d.ID, d.Name)
			if warn != nil {
				res.Warnings = append(res.Warnings, *warn)
				continue
			}
			d.Items = append(d.Items, item)
		}
		res.Register.Drivers = append(res.Register.Drivers, d)
	}
	return res, nil
}

// item builds one dated item from a cell. A blank cell still produces an item,
// with no date, so that the report can say "not recorded" instead of staying
// quiet about an obligation nobody has filled in.
func (t *table) item(row []string, col string, kind fleet.Kind, line int,
	st fleet.SubjectType, id, name string) (fleet.Item, *Warning) {

	item := fleet.Item{
		SubjectType: st,
		SubjectID:   id,
		SubjectName: name,
		Kind:        kind,
		Source:      fleet.SourceManual,
	}
	raw := t.cell(row, col)
	if raw == "" {
		if apiBacked[kind] && st == fleet.SubjectVehicle {
			return pendingItem(id, name, kind), nil
		}
		return item, nil
	}
	d, err := time.Parse(DateLayout, raw)
	if err != nil {
		return item, &Warning{File: t.name, Line: line,
			Err: fmt.Errorf("%s: %q is not a %s date", col, raw, DateLayout)}
	}
	item.Expires = d
	return item, nil
}

// WriteVehicles writes a register back out in vehicles.csv shape, so that a
// nightly API pull produces a file the operator can still open in a spreadsheet
// and edit by hand.
func WriteVehicles(w io.Writer, r fleet.Register) error {
	cw := csv.NewWriter(w)
	header := []string{"plate", "fleet_number", "make", "model"}
	for _, c := range VehicleColumns {
		header = append(header, c.name)
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, v := range r.Vehicles {
		byKind := map[fleet.Kind]fleet.Item{}
		for _, it := range v.Items {
			byKind[it.Kind] = it
		}
		row := []string{v.Plate, v.FleetNumber, v.Make, v.Model}
		for _, c := range VehicleColumns {
			if it, ok := byKind[c.kind]; ok && it.Recorded() {
				row = append(row, it.Expires.Format(DateLayout))
				continue
			}
			row = append(row, "")
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// SetItem replaces the vehicle's item of the same kind, or appends it.
func SetItem(v *fleet.Vehicle, item fleet.Item) {
	for i := range v.Items {
		if v.Items[i].Kind == item.Kind {
			v.Items[i] = item
			return
		}
	}
	v.Items = append(v.Items, item)
}

// pendingItem is an API-backed item with no date yet.
func pendingItem(id, name string, kind fleet.Kind) fleet.Item {
	return fleet.Item{
		SubjectType: fleet.SubjectVehicle,
		SubjectID:   id,
		SubjectName: name,
		Kind:        kind,
		Source:      fleet.SourcePending,
		Note:        "awaiting API pull",
	}
}

// Merge folds drivers from one result into another, combining warnings.
func (r *Result) Merge(other Result) {
	r.Register.Vehicles = append(r.Register.Vehicles, other.Register.Vehicles...)
	r.Register.Drivers = append(r.Register.Drivers, other.Register.Drivers...)
	r.Warnings = append(r.Warnings, other.Warnings...)
}
