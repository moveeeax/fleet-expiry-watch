package expiry

import (
	"testing"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
)

var today = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func item(expires time.Time) fleet.Item {
	return fleet.Item{
		SubjectType: fleet.SubjectVehicle,
		SubjectID:   "AB12CDE",
		SubjectName: "AB12 CDE",
		Kind:        fleet.KindMOT,
		Expires:     expires,
		Source:      fleet.SourceManual,
	}
}

func TestDaysUntilIgnoresClockTime(t *testing.T) {
	// An expiry stamped late in the evening is still "tomorrow", not 0 days.
	expires := time.Date(2026, 7, 2, 23, 30, 0, 0, time.UTC)
	now := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)
	if got := DaysUntil(expires, now); got != 1 {
		t.Fatalf("DaysUntil = %d, want 1", got)
	}
}

func TestDaysUntilAcrossDST(t *testing.T) {
	// 29 March 2026 is the UK clock change. The day count must not slip.
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("no tzdata available: %v", err)
	}
	from := time.Date(2026, 3, 28, 12, 0, 0, 0, london)
	to := time.Date(2026, 3, 30, 9, 0, 0, 0, london)
	if got := DaysUntil(to, from); got != 2 {
		t.Fatalf("DaysUntil across DST = %d, want 2", got)
	}
}

func TestAssessLadder(t *testing.T) {
	cases := []struct {
		name    string
		expires time.Time
		days    int
		level   Level
		stage   Stage
		alert   bool
	}{
		{"well clear", at(2027, 3, 18), 260, LevelGreen, StageNone, false},
		{"one day before the ladder", at(2026, 8, 31), 61, LevelGreen, StageNone, false},
		{"exactly 60 days", at(2026, 8, 30), 60, LevelAmber, StageAt(60), true},
		{"between 60 and 30", at(2026, 8, 15), 45, LevelAmber, StageAt(60), false},
		{"exactly 30 days", at(2026, 7, 31), 30, LevelAmber, StageAt(30), true},
		{"exactly 14 days", at(2026, 7, 15), 14, LevelAmber, StageAt(14), true},
		{"exactly 7 days", at(2026, 7, 8), 7, LevelAmber, StageAt(7), true},
		{"two days out", at(2026, 7, 3), 2, LevelAmber, StageAt(7), false},
		{"exactly 1 day", at(2026, 7, 2), 1, LevelAmber, StageAt(1), true},
		{"expires today", at(2026, 7, 1), 0, LevelRed, StageLapsed, true},
		{"lapsed", at(2026, 6, 12), -19, LevelRed, StageLapsed, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := Assess(item(c.expires), today)
			if a.Days != c.days {
				t.Errorf("Days = %d, want %d", a.Days, c.days)
			}
			if a.Level != c.level {
				t.Errorf("Level = %v, want %v", a.Level, c.level)
			}
			if a.Stage != c.stage {
				t.Errorf("Stage = %q, want %q", a.Stage, c.stage)
			}
			if a.AlertDay != c.alert {
				t.Errorf("AlertDay = %v, want %v", a.AlertDay, c.alert)
			}
		})
	}
}

func TestAssessUnrecorded(t *testing.T) {
	a := Assess(fleet.Item{Kind: fleet.KindInsurance}, today)
	if a.Level != LevelUnknown {
		t.Errorf("Level = %v, want LevelUnknown", a.Level)
	}
	if a.Stage != StageUnknown {
		t.Errorf("Stage = %q, want %q", a.Stage, StageUnknown)
	}
	if a.AlertDay {
		t.Error("AlertDay = true, want false: an item with no date cannot escalate")
	}
	if got, want := a.Summary(), "no date recorded"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}

func TestSummary(t *testing.T) {
	cases := []struct {
		expires time.Time
		want    string
	}{
		{at(2026, 6, 30), "lapsed 1 day ago"},
		{at(2026, 6, 12), "lapsed 19 days ago"},
		{at(2026, 7, 1), "expires today"},
		{at(2026, 7, 2), "1 day left"},
		{at(2026, 7, 31), "30 days left"},
	}
	for _, c := range cases {
		if got := Assess(item(c.expires), today).Summary(); got != c.want {
			t.Errorf("Summary(%s) = %q, want %q", c.expires.Format("2006-01-02"), got, c.want)
		}
	}
}

func TestShouldNotifyDeduplicates(t *testing.T) {
	// T-30 day, and the recipient has already been told about T-30.
	a := Assess(item(at(2026, 7, 31)), today)
	if a.Stage != StageAt(30) {
		t.Fatalf("precondition: stage = %q, want T-30", a.Stage)
	}
	if ShouldNotify(a, StageAt(30)) {
		t.Error("notified twice for the same stage: de-duplication is not working")
	}
	if !ShouldNotify(a, StageAt(60)) {
		t.Error("did not notify on a new stage")
	}
}

func TestShouldNotifyQuietBetweenThresholds(t *testing.T) {
	a := Assess(item(at(2026, 8, 15)), today) // 45 days: on the ladder, not a threshold day
	if ShouldNotify(a, StageNone) {
		t.Error("notified on a non-threshold day")
	}
}

func TestShouldNotifyDailyWhileLapsed(t *testing.T) {
	a := Assess(item(at(2026, 6, 12)), today)
	if !ShouldNotify(a, StageLapsed) {
		t.Error("stopped notifying about a lapsed item; lapsed items must alert daily")
	}
}

func TestShouldNotifyNeverForUnknown(t *testing.T) {
	a := Assess(fleet.Item{Kind: fleet.KindADR}, today)
	if ShouldNotify(a, StageNone) {
		t.Error("notified about an item with no date")
	}
}

func TestDigestSplit(t *testing.T) {
	items := []fleet.Item{
		item(at(2026, 6, 12)),   // lapsed
		item(at(2026, 7, 15)),   // 14 days
		item(at(2026, 8, 30)),   // 60 days, outside a 30-day window
		{Kind: fleet.KindLOLER}, // no date
	}
	lapsed, due := Digest(AssessAll(items, today), 30)
	if len(lapsed) != 1 {
		t.Errorf("lapsed = %d, want 1", len(lapsed))
	}
	if len(due) != 1 {
		t.Errorf("due within 30 days = %d, want 1", len(due))
	}
}
