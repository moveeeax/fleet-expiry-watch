package plate

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in         string
		normalised string
		display    string
		format     Format
	}{
		{"AB12CDE", "AB12CDE", "AB12 CDE", FormatCurrent},
		{"ab12 cde", "AB12CDE", "AB12 CDE", FormatCurrent},
		{" bd19-klm ", "BD19KLM", "BD19 KLM", FormatCurrent},
		{"A123BCD", "A123BCD", "A123 BCD", FormatPrefix},
		{"ABC123A", "ABC123A", "ABC 123A", FormatSuffix},
		{"123ABC", "123ABC", "123 ABC", FormatDateless},
		{"K9", "K9", "K 9", FormatDateless},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			m, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", c.in, err)
			}
			if m.Normalised != c.normalised {
				t.Errorf("normalised = %q, want %q", m.Normalised, c.normalised)
			}
			if got := m.String(); got != c.display {
				t.Errorf("String() = %q, want %q", got, c.display)
			}
			if m.Format != c.format {
				t.Errorf("format = %q, want %q", m.Format, c.format)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "   ", ErrEmpty},
		{"punctuation only", "!!!", ErrEmpty},
		{"northern ireland", "GAZ 1234", ErrNorthernIreland},
		{"northern ireland no space", "ABZ4567", ErrNorthernIreland},
		{"too long", "AB12CDEFG", nil},
		{"not a plate at all", "VOLVO FH", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.in)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", c.in)
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("Parse(%q) error = %v, want %v", c.in, err, c.want)
			}
		})
	}
}

func TestNormaliseNeverPanics(t *testing.T) {
	for _, in := range []string{"", "  ", "🚚", "a-b-c-1-2-3", "AB12CDE"} {
		if got := Normalise(in); got != "" && !isUpperAlnum(got) {
			t.Errorf("Normalise(%q) = %q, which is not upper alphanumeric", in, got)
		}
	}
}

func isUpperAlnum(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
