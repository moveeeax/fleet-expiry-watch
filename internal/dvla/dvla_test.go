package dvla

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
)

type fakeVES struct {
	*httptest.Server
	lastKey  string
	lastBody map[string]string
	fixture  string
	status   int
	body     string
}

func newFakeVES(t *testing.T, fixture string) *fakeVES {
	t.Helper()
	f := &fakeVES{fixture: fixture, status: http.StatusOK}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/vehicles" {
			t.Errorf("path = %s, want /vehicles", r.URL.Path)
		}
		f.lastKey = r.Header.Get("x-api-key")
		f.lastBody = map[string]string{}
		if err := json.NewDecoder(r.Body).Decode(&f.lastBody); err != nil {
			t.Errorf("request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(f.body))
			return
		}
		body, err := os.ReadFile("testdata/" + f.fixture)
		if err != nil {
			t.Errorf("reading fixture: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.Close)
	return f
}

func clientFor(f *fakeVES) *Client {
	return &Client{BaseURL: f.URL, APIKey: "test-api-key", HTTP: f.Client()}
}

func TestNewWithoutKey(t *testing.T) {
	t.Setenv("FLEETWATCH_DVLA_API_KEY", "")
	if _, err := New(); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("error = %v, want ErrNoCredentials", err)
	}
}

func TestNewReadsEnvironment(t *testing.T) {
	t.Setenv("FLEETWATCH_DVLA_API_KEY", "placeholder-key")
	t.Setenv("FLEETWATCH_DVLA_BASE_URL", "https://example.invalid/ves/v1/")
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.BaseURL != "https://example.invalid/ves/v1" {
		t.Errorf("BaseURL = %q, want the trailing slash trimmed", c.BaseURL)
	}
}

func TestLookupTaxedVehicle(t *testing.T) {
	f := newFakeVES(t, "taxed.json")
	v, err := clientFor(f).Lookup(context.Background(), "AB12CDE")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if f.lastKey != "test-api-key" {
		t.Errorf("x-api-key = %q, want the configured key", f.lastKey)
	}
	if got := f.lastBody["registrationNumber"]; got != "AB12CDE" {
		t.Errorf("registrationNumber sent = %q, want AB12CDE", got)
	}
	if v.TaxStatus != "Taxed" || v.TaxDueDate != "2026-08-01" {
		t.Errorf("decoded %+v, want Taxed / 2026-08-01", v)
	}

	items := v.Items("AB12CDE", "AB12 CDE")
	if len(items) != 2 {
		t.Fatalf("items = %d, want tax plus MOT", len(items))
	}
	tax := items[0]
	if tax.Kind != fleet.KindTax || tax.Source != fleet.SourceDVLA {
		t.Errorf("first item = %+v, want a DVLA tax item", tax)
	}
	if want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC); !tax.Expires.Equal(want) {
		t.Errorf("tax due = %v, want %v", tax.Expires, want)
	}
}

func TestLookupSORNVehicleHasNoTaxDate(t *testing.T) {
	f := newFakeVES(t, "sorn.json")
	v, err := clientFor(f).Lookup(context.Background(), "FJ18XYZ")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	items := v.Items("FJ18XYZ", "FJ18 XYZ")
	tax := items[0]
	if tax.Recorded() {
		t.Errorf("a SORN vehicle has no tax due date, got %v", tax.Expires)
	}
	if tax.Note != "tax status: sorn" {
		t.Errorf("note = %q, want the SORN status carried through", tax.Note)
	}
}

func TestLookupNotFound(t *testing.T) {
	f := newFakeVES(t, "taxed.json")
	f.status = http.StatusNotFound
	f.body = `{"errors":[{"status":"404","code":"404","title":"Vehicle Not Found"}]}`
	if _, err := clientFor(f).Lookup(context.Background(), "AB12CDE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestLookupSurfacesAPIError(t *testing.T) {
	f := newFakeVES(t, "taxed.json")
	f.status = http.StatusTooManyRequests
	f.body = `{"errors":[{"status":"429","code":"429","title":"Too Many Requests","detail":"Rate limit exceeded"}]}`
	_, err := clientFor(f).Lookup(context.Background(), "AB12CDE")
	if err == nil {
		t.Fatal("a 429 was reported as success")
	}
	for _, want := range []string{"Too Many Requests", "Rate limit exceeded"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "test-api-key") {
		t.Errorf("error message leaks the API key: %q", err)
	}
}
