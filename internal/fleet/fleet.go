// Package fleet holds the data model: the vehicles, the drivers and the dated
// compliance items hanging off each of them.
package fleet

import (
	"sort"
	"time"
)

// Kind identifies a dated compliance item.
type Kind string

// Vehicle items. MOT and tax are populated from the DVSA/DVLA APIs; the rest
// are entered by hand because no free API exposes them.
const (
	KindMOT              Kind = "mot"
	KindTax              Kind = "tax"
	KindInsurance        Kind = "insurance"
	KindOperatorLicence  Kind = "olicence"
	KindTachoCalibration Kind = "tacho_calibration"
	KindADR              Kind = "adr"
	KindLOLER            Kind = "loler"
)

// Driver items. All manual: DVLA driver licence checking needs per-driver
// consent and a separate access route, which is out of scope.
const (
	KindDriverLicence Kind = "driver_licence"
	KindDriverCPC     Kind = "driver_cpc"
	KindTachoCard     Kind = "tacho_card"
	KindDriverMedical Kind = "driver_medical"
)

// Label is the human-readable name of the item kind.
func (k Kind) Label() string {
	switch k {
	case KindMOT:
		return "MOT"
	case KindTax:
		return "Vehicle tax"
	case KindInsurance:
		return "Insurance"
	case KindOperatorLicence:
		return "O-licence disc"
	case KindTachoCalibration:
		return "Tacho calibration"
	case KindADR:
		return "ADR certificate"
	case KindLOLER:
		return "LOLER inspection"
	case KindDriverLicence:
		return "Driver licence"
	case KindDriverCPC:
		return "Driver CPC (Code 95)"
	case KindTachoCard:
		return "Digital tacho card"
	case KindDriverMedical:
		return "Driver medical"
	}
	return string(k)
}

// Source records where a date came from, which matters for attribution and for
// knowing what a nightly pull is allowed to overwrite.
type Source string

const (
	// SourceManual is a date typed in by the transport manager.
	SourceManual Source = "manual"
	// SourceDVSA is the DVSA MOT History API.
	SourceDVSA Source = "dvsa"
	// SourceDVLA is the DVLA Vehicle Enquiry Service API.
	SourceDVLA Source = "dvla"
	// SourcePending marks an item an API pull is expected to fill in but has
	// not yet: it is listed so the operator can see the gap, not hidden.
	SourcePending Source = "pending"
)

// SubjectType distinguishes a vehicle row from a driver row.
type SubjectType string

const (
	SubjectVehicle SubjectType = "vehicle"
	SubjectDriver  SubjectType = "driver"
)

// Vehicle is one vehicle on the operator licence.
type Vehicle struct {
	Plate       string // normalised registration mark
	Display     string // spaced registration mark, for printing
	FleetNumber string
	Make        string
	Model       string
	Items       []Item
}

// Driver is one driver employed against the licence.
type Driver struct {
	ID    string
	Name  string
	Items []Item
}

// Item is a single dated obligation. A zero Expires means "not recorded",
// which is reported as unknown rather than silently treated as compliant.
type Item struct {
	SubjectType SubjectType
	SubjectID   string // normalised plate or driver ID
	SubjectName string // display plate or driver name
	Kind        Kind
	Expires     time.Time
	Source      Source
	Note        string
}

// Recorded reports whether the item has a date at all.
func (i Item) Recorded() bool { return !i.Expires.IsZero() }

// Register is the whole fleet: vehicles, drivers and their items.
type Register struct {
	Vehicles []Vehicle
	Drivers  []Driver
}

// Items flattens the register into a single slice of dated items, ordered by
// expiry date with unrecorded items last.
func (r Register) Items() []Item {
	var out []Item
	for _, v := range r.Vehicles {
		out = append(out, v.Items...)
	}
	for _, d := range r.Drivers {
		out = append(out, d.Items...)
	}
	sort.SliceStable(out, func(a, b int) bool {
		ia, ib := out[a], out[b]
		switch {
		case !ia.Recorded() && !ib.Recorded():
			// fall through to the name/kind tiebreak
		case !ia.Recorded():
			return false
		case !ib.Recorded():
			return true
		case !ia.Expires.Equal(ib.Expires):
			return ia.Expires.Before(ib.Expires)
		}
		if ia.SubjectName != ib.SubjectName {
			return ia.SubjectName < ib.SubjectName
		}
		return ia.Kind < ib.Kind
	})
	return out
}
