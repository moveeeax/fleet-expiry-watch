// Package expiry turns a dated compliance item into a traffic-light status and
// decides whether today is an alert day for it.
//
// The ladder is 60 / 30 / 14 / 7 / 1 days before expiry, then daily for as long
// as the item stays lapsed.
package expiry

import (
	"fmt"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
)

// Thresholds are the lead times, in days, at which an alert fires. They must
// stay in descending order.
var Thresholds = []int{60, 30, 14, 7, 1}

// Level is the traffic-light colour of an item.
type Level int

const (
	// LevelUnknown means no date has been recorded for the item.
	LevelUnknown Level = iota
	// LevelGreen means the item is outside the whole escalation ladder.
	LevelGreen
	// LevelAmber means the item is somewhere on the ladder but still valid.
	LevelAmber
	// LevelRed means the item expires today or has already lapsed.
	LevelRed
)

// String returns the short uppercase name used in reports.
func (l Level) String() string {
	switch l {
	case LevelGreen:
		return "OK"
	case LevelAmber:
		return "DUE"
	case LevelRed:
		return "LAPSED"
	}
	return "UNKNOWN"
}

// Stage is the rung of the escalation ladder an item currently sits on.
type Stage string

const (
	// StageNone means the item has not yet reached the first threshold.
	StageNone Stage = "none"
	// StageLapsed means the item has expired and is alerted on daily.
	StageLapsed Stage = "lapsed"
	// StageUnknown means there is no date to escalate against.
	StageUnknown Stage = "unknown"
)

// StageAt names the rung for a lead time, e.g. "T-30".
func StageAt(days int) Stage { return Stage(fmt.Sprintf("T-%d", days)) }

// Assessment is the verdict on one item on one day.
type Assessment struct {
	Item fleet.Item
	// Days is the whole days from today until expiry. Negative once lapsed.
	// Meaningless when Level is LevelUnknown.
	Days int
	// Level is the traffic-light colour.
	Level Level
	// Stage is the ladder rung reached.
	Stage Stage
	// AlertDay reports whether the ladder fires on this exact day.
	AlertDay bool
}

// Summary is a one-line description of the assessment, for tables and email.
func (a Assessment) Summary() string {
	switch {
	case a.Level == LevelUnknown:
		return "no date recorded"
	case a.Days < 0:
		return fmt.Sprintf("lapsed %s ago", days(-a.Days))
	case a.Days == 0:
		return "expires today"
	}
	return fmt.Sprintf("%s left", days(a.Days))
}

func days(n int) string {
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

// DaysUntil returns whole calendar days from today to expiry, ignoring clock
// time and time zone offsets so that "tomorrow" is always exactly 1.
func DaysUntil(expires, today time.Time) int {
	e := time.Date(expires.Year(), expires.Month(), expires.Day(), 0, 0, 0, 0, time.UTC)
	t := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	return int(e.Sub(t).Hours() / 24)
}

// Assess evaluates one item as at the given day.
func Assess(item fleet.Item, today time.Time) Assessment {
	if !item.Recorded() {
		return Assessment{Item: item, Level: LevelUnknown, Stage: StageUnknown}
	}
	d := DaysUntil(item.Expires, today)
	a := Assessment{Item: item, Days: d, Stage: stageFor(d)}
	switch {
	case d <= 0:
		a.Level = LevelRed
	case d <= Thresholds[0]:
		a.Level = LevelAmber
	default:
		a.Level = LevelGreen
	}
	a.AlertDay = alertDay(d)
	return a
}

func stageFor(d int) Stage {
	if d <= 0 {
		return StageLapsed
	}
	for _, t := range Thresholds {
		if d <= t {
			continue
		}
		// d sits above this threshold, so the rung is the previous one.
		return higherRung(t)
	}
	return StageAt(Thresholds[len(Thresholds)-1])
}

// higherRung returns the rung immediately coarser than t, or StageNone if t is
// already the coarsest threshold.
func higherRung(t int) Stage {
	for i, v := range Thresholds {
		if v == t {
			if i == 0 {
				return StageNone
			}
			return StageAt(Thresholds[i-1])
		}
	}
	return StageNone
}

// alertDay reports whether the ladder fires today: on each threshold day
// exactly, and then every day from expiry onwards.
func alertDay(d int) bool {
	if d <= 0 {
		return true
	}
	for _, t := range Thresholds {
		if d == t {
			return true
		}
	}
	return false
}

// ShouldNotify decides whether to send an alert for an item, given the stage
// the recipient was last told about. It suppresses repeat mail for an unchanged
// item, which is the de-duplication rule carried over from uptime-ping, while
// still mailing daily once something has actually lapsed.
func ShouldNotify(a Assessment, lastNotified Stage) bool {
	if a.Level == LevelUnknown {
		return false
	}
	if a.Stage == StageLapsed {
		return true
	}
	if !a.AlertDay {
		return false
	}
	return a.Stage != lastNotified
}

// AssessAll evaluates every item as at the given day, keeping the caller's
// ordering (the register sorts soonest expiry first).
func AssessAll(items []fleet.Item, today time.Time) []Assessment {
	out := make([]Assessment, 0, len(items))
	for _, it := range items {
		out = append(out, Assess(it, today))
	}
	return out
}

// Digest splits assessments into the two lists the Monday morning mail needs:
// items already lapsed, and items expiring within the window.
func Digest(all []Assessment, window int) (lapsed, dueSoon []Assessment) {
	for _, a := range all {
		switch {
		case a.Level == LevelUnknown:
			continue
		case a.Days < 0:
			lapsed = append(lapsed, a)
		case a.Days <= window:
			dueSoon = append(dueSoon, a)
		}
	}
	return lapsed, dueSoon
}
