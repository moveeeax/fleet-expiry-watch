// Package plate normalises and validates GB vehicle registration marks.
//
// Only Great Britain marks are in scope. Northern Ireland marks are recognised
// so that they can be rejected with a useful message rather than being sent to
// the DVSA/DVLA APIs, which do not serve them through these endpoints.
package plate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Format is the registration scheme a mark belongs to.
type Format string

const (
	// FormatCurrent is the 2001-onwards scheme, e.g. AB12CDE.
	FormatCurrent Format = "current"
	// FormatPrefix is the 1983-2001 scheme, e.g. A123BCD.
	FormatPrefix Format = "prefix"
	// FormatSuffix is the 1963-1983 scheme, e.g. ABC123A.
	FormatSuffix Format = "suffix"
	// FormatDateless covers pre-1963 and personalised dateless marks, e.g. 123ABC.
	FormatDateless Format = "dateless"
)

// ErrEmpty is returned when a mark contains no usable characters.
var ErrEmpty = errors.New("registration is empty")

// ErrNorthernIreland is returned for NI-style marks, which are out of scope.
var ErrNorthernIreland = errors.New("Northern Ireland registration: out of scope for this tool")

var (
	stripRE    = regexp.MustCompile(`[^A-Z0-9]`)
	currentRE  = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z]{3}$`)
	prefixRE   = regexp.MustCompile(`^[A-Z][0-9]{1,3}[A-Z]{3}$`)
	suffixRE   = regexp.MustCompile(`^[A-Z]{3}[0-9]{1,3}[A-Z]$`)
	datelessRE = regexp.MustCompile(`^([A-Z]{1,3}[0-9]{1,4}|[0-9]{1,4}[A-Z]{1,3})$`)
	// NI marks are three letters including an I or a Z, then up to four digits.
	northernIrelandRE = regexp.MustCompile(`^[A-Z]{2}[IZ][0-9]{1,4}$|^[A-Z][IZ][A-Z][0-9]{1,4}$`)
)

// Mark is a validated GB registration mark.
type Mark struct {
	// Normalised is the mark with all separators removed and upper-cased.
	Normalised string
	// Format is the scheme the mark was recognised as.
	Format Format
}

// String renders the mark in the spaced form used on the plate itself.
func (m Mark) String() string {
	s := m.Normalised
	switch m.Format {
	case FormatCurrent:
		// AB12 CDE: area code and age identifier, then the random letters.
		return s[:4] + " " + s[4:]
	case FormatPrefix:
		// A123 BCD: the space sits before the final three letters.
		return s[:len(s)-3] + " " + s[len(s)-3:]
	case FormatSuffix:
		// ABC 123A: the space sits after the first three letters.
		return s[:3] + " " + s[3:]
	case FormatDateless:
		if i := splitPoint(s); i > 0 {
			return s[:i] + " " + s[i:]
		}
	}
	return s
}

// splitPoint returns the index of the first letter/digit boundary after the
// leading run, which is where GB plates carry their space.
func splitPoint(s string) int {
	if len(s) < 2 {
		return 0
	}
	leadDigit := isDigit(s[0])
	for i := 1; i < len(s); i++ {
		if isDigit(s[i]) != leadDigit {
			return i
		}
	}
	return 0
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// Normalise strips separators and upper-cases a registration mark. It performs
// no validation and is safe to call on user input of any shape.
func Normalise(raw string) string {
	return stripRE.ReplaceAllString(strings.ToUpper(strings.TrimSpace(raw)), "")
}

// Parse normalises and validates a registration mark.
func Parse(raw string) (Mark, error) {
	n := Normalise(raw)
	if n == "" {
		return Mark{}, ErrEmpty
	}
	if len(n) > 7 {
		return Mark{}, fmt.Errorf("%q: too long for a GB registration", raw)
	}
	if northernIrelandRE.MatchString(n) {
		return Mark{}, fmt.Errorf("%s: %w", n, ErrNorthernIreland)
	}
	switch {
	case currentRE.MatchString(n):
		return Mark{Normalised: n, Format: FormatCurrent}, nil
	case prefixRE.MatchString(n):
		return Mark{Normalised: n, Format: FormatPrefix}, nil
	case suffixRE.MatchString(n):
		return Mark{Normalised: n, Format: FormatSuffix}, nil
	case datelessRE.MatchString(n):
		return Mark{Normalised: n, Format: FormatDateless}, nil
	}
	return Mark{}, fmt.Errorf("%q: not a recognised GB registration format", raw)
}
