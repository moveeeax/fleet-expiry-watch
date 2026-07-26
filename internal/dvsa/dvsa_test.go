package dvsa

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
)

// fakeAPI stands in for the DVSA token endpoint and the MOT History endpoint.
type fakeAPI struct {
	*httptest.Server
	tokenCalls  atomic.Int32
	lookupCalls atomic.Int32
	lastAuth    string
	lastAPIKey  string
	fixture     string
	status      int
}

func newFakeAPI(t *testing.T, fixture string) *fakeAPI {
	t.Helper()
	f := &fakeAPI{fixture: fixture, status: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenCalls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("token request body was not a form: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q, want client_credentials", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/vehicles/registration/", func(w http.ResponseWriter, r *http.Request) {
		f.lookupCalls.Add(1)
		f.lastAuth = r.Header.Get("Authorization")
		f.lastAPIKey = r.Header.Get("X-API-Key")
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		body, err := os.ReadFile("testdata/" + f.fixture)
		if err != nil {
			t.Errorf("reading fixture: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func clientFor(t *testing.T, f *fakeAPI) *Client {
	t.Helper()
	c, err := NewWithConfig(Config{
		BaseURL:      f.URL,
		TokenURL:     f.URL + "/token",
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		APIKey:       "test-api-key",
	})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	return c
}

func TestNewRequiresEveryCredential(t *testing.T) {
	base := Config{
		TokenURL:     "https://example.invalid/token",
		ClientID:     "id",
		ClientSecret: "secret",
		APIKey:       "key",
	}
	for _, missing := range []string{"TokenURL", "ClientID", "ClientSecret", "APIKey"} {
		t.Run(missing, func(t *testing.T) {
			cfg := base
			switch missing {
			case "TokenURL":
				cfg.TokenURL = ""
			case "ClientID":
				cfg.ClientID = ""
			case "ClientSecret":
				cfg.ClientSecret = ""
			case "APIKey":
				cfg.APIKey = ""
			}
			if _, err := NewWithConfig(cfg); !errors.Is(err, ErrNoCredentials) {
				t.Fatalf("error = %v, want ErrNoCredentials", err)
			}
		})
	}
}

func TestLookupSendsTokenAndKey(t *testing.T) {
	f := newFakeAPI(t, "tested.json")
	c := clientFor(t, f)

	v, err := c.Lookup(context.Background(), "AB12CDE")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if v.Registration != "AB12CDE" || v.Make != "VOLVO" {
		t.Errorf("decoded %+v, want registration AB12CDE make VOLVO", v)
	}
	if f.lastAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", f.lastAuth, "Bearer test-token")
	}
	if f.lastAPIKey != "test-api-key" {
		t.Errorf("X-API-Key = %q, want the configured key", f.lastAPIKey)
	}
}

func TestTokenIsCachedAcrossLookups(t *testing.T) {
	f := newFakeAPI(t, "tested.json")
	c := clientFor(t, f)
	for i := 0; i < 5; i++ {
		if _, err := c.Lookup(context.Background(), "AB12CDE"); err != nil {
			t.Fatalf("Lookup %d: %v", i, err)
		}
	}
	if got := f.tokenCalls.Load(); got != 1 {
		t.Errorf("token requests = %d, want 1: a 50-vehicle pull must not ask for 50 tokens", got)
	}
	if got := f.lookupCalls.Load(); got != 5 {
		t.Errorf("lookups = %d, want 5", got)
	}
}

func TestMOTExpiryUsesLatestPassedTest(t *testing.T) {
	f := newFakeAPI(t, "tested.json")
	c := clientFor(t, f)
	v, err := c.Lookup(context.Background(), "AB12CDE")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	got, ok := v.MOTExpiry()
	if !ok {
		t.Fatal("MOTExpiry reported no date for a vehicle with two passed tests")
	}
	// The fixture holds a 2024 pass, a 2025 failure and a 2025 pass. Only the
	// most recent pass counts, and the failure carries no expiry at all.
	want := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("MOTExpiry = %v, want %v", got, want)
	}

	item := v.Item("AB12CDE", "AB12 CDE")
	if item.Kind != fleet.KindMOT || item.Source != fleet.SourceDVSA {
		t.Errorf("item = %+v, want an MOT item sourced from DVSA", item)
	}
	if !item.Expires.Equal(want) {
		t.Errorf("item.Expires = %v, want %v", item.Expires, want)
	}
}

func TestMOTExpiryFallsBackToFirstTestDue(t *testing.T) {
	f := newFakeAPI(t, "never_tested.json")
	c := clientFor(t, f)
	v, err := c.Lookup(context.Background(), "FJ18XYZ")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	got, ok := v.MOTExpiry()
	if !ok {
		t.Fatal("a never-tested vehicle still has a first-test deadline")
	}
	want := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("MOTExpiry = %v, want the motTestDueDate %v", got, want)
	}
	if note := v.Item("FJ18XYZ", "FJ18 XYZ").Note; !strings.Contains(note, "never tested") {
		t.Errorf("note = %q, want it to flag that the vehicle has never been tested", note)
	}
}

func TestLookupNotFound(t *testing.T) {
	f := newFakeAPI(t, "tested.json")
	f.status = http.StatusNotFound
	c := clientFor(t, f)
	if _, err := c.Lookup(context.Background(), "AB12CDE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestLookupServerError(t *testing.T) {
	f := newFakeAPI(t, "tested.json")
	f.status = http.StatusInternalServerError
	c := clientFor(t, f)
	_, err := c.Lookup(context.Background(), "AB12CDE")
	if err == nil {
		t.Fatal("a 500 was reported as success")
	}
	if strings.Contains(err.Error(), "test-api-key") || strings.Contains(err.Error(), "test-client-secret") {
		t.Errorf("error message leaks a credential: %q", err)
	}
}
