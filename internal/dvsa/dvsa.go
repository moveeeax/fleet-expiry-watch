// Package dvsa is a client for the DVSA MOT History API, which supplies the
// authoritative MOT expiry date for a GB vehicle.
//
// The API needs two things: an OAuth2 client-credentials token from the DVSA
// tenant, and an API key sent in X-API-Key. Both are free but must be applied
// for. With either missing, New returns ErrNoCredentials.
package dvsa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
)

// DefaultBaseURL is the live MOT History trade endpoint.
const DefaultBaseURL = "https://history.mot.api.gov.uk/v1/trade"

// DefaultScope is the OAuth2 scope DVSA issues tokens against.
const DefaultScope = "https://tapi.dvsa.gov.uk/.default"

// ErrNoCredentials means the client id, secret, token URL or API key is missing.
var ErrNoCredentials = errors.New("dvsa: incomplete credentials (need FLEETWATCH_DVSA_CLIENT_ID, " +
	"FLEETWATCH_DVSA_CLIENT_SECRET, FLEETWATCH_DVSA_TOKEN_URL and FLEETWATCH_DVSA_API_KEY)")

// ErrNotFound means DVSA holds no MOT history for that registration.
var ErrNotFound = errors.New("dvsa: no MOT history for registration")

// Config is everything needed to reach the API.
type Config struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	APIKey       string
	Scope        string
}

// Client fetches MOT history. It caches the bearer token until shortly before
// it expires, because DVSA rate-limits token issuance far harder than lookups.
type Client struct {
	cfg  Config
	http *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// New builds a client from the environment.
func New() (*Client, error) {
	cfg := Config{
		BaseURL:      env("FLEETWATCH_DVSA_BASE_URL", DefaultBaseURL),
		TokenURL:     env("FLEETWATCH_DVSA_TOKEN_URL", ""),
		ClientID:     env("FLEETWATCH_DVSA_CLIENT_ID", ""),
		ClientSecret: env("FLEETWATCH_DVSA_CLIENT_SECRET", ""),
		APIKey:       env("FLEETWATCH_DVSA_API_KEY", ""),
		Scope:        env("FLEETWATCH_DVSA_SCOPE", DefaultScope),
	}
	return NewWithConfig(cfg)
}

// NewWithConfig builds a client from an explicit config, which is how tests
// point it at a local server.
func NewWithConfig(cfg Config) (*Client, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.TokenURL == "" || cfg.APIKey == "" {
		return nil, ErrNoCredentials
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Scope == "" {
		cfg.Scope = DefaultScope
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: &http.Client{Timeout: 20 * time.Second}}, nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// accessToken returns a cached bearer token, fetching a new one when needed.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"scope":         {c.cfg.Scope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("dvsa: token request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		// The body of a token error can echo the client secret back; never log it.
		return "", fmt.Errorf("dvsa: token request failed with status %d", resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", fmt.Errorf("dvsa: decoding token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", errors.New("dvsa: token response contained no access_token")
	}
	c.token = tr.AccessToken
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	// Refresh a minute early so a long batch never uses a token mid-expiry.
	c.expires = time.Now().Add(ttl - time.Minute)
	return c.token, nil
}

// MOTTest is one recorded test.
type MOTTest struct {
	CompletedDate string `json:"completedDate"`
	TestResult    string `json:"testResult"`
	ExpiryDate    string `json:"expiryDate"`
	OdometerValue string `json:"odometerValue"`
	OdometerUnit  string `json:"odometerUnit"`
	MotTestNumber string `json:"motTestNumber"`
}

// VehicleResponse is the subset of the MOT History payload this tool uses.
type VehicleResponse struct {
	Registration string `json:"registration"`
	Make         string `json:"make"`
	Model        string `json:"model"`
	FirstUsed    string `json:"firstUsedDate"`
	// MotTestDueDate is set for vehicles that have never been tested yet.
	MotTestDueDate string    `json:"motTestDueDate"`
	MotTests       []MOTTest `json:"motTests"`
}

// Lookup fetches the MOT history for one normalised registration mark.
func (c *Client) Lookup(ctx context.Context, plate string) (*VehicleResponse, error) {
	tok, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := c.cfg.BaseURL + "/vehicles/registration/" + url.PathEscape(plate)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-API-Key", c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dvsa: %s: %w", plate, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var v VehicleResponse
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("dvsa: %s: decoding response: %w", plate, err)
		}
		return &v, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", plate, ErrNotFound)
	default:
		return nil, fmt.Errorf("dvsa: %s: unexpected status %d", plate, resp.StatusCode)
	}
}

// MOTExpiry returns the date the current MOT runs out, and whether one is known.
//
// The rule is: the expiry date of the most recently completed passed test. If
// the vehicle has never been tested, DVSA gives motTestDueDate instead, which
// is the date the first test is due — an equally hard deadline for the operator.
func (v *VehicleResponse) MOTExpiry() (time.Time, bool) {
	var best time.Time
	var bestCompleted time.Time
	for _, t := range v.MotTests {
		if !strings.EqualFold(t.TestResult, "PASSED") {
			continue
		}
		exp, err := time.Parse("2006-01-02", t.ExpiryDate)
		if err != nil {
			continue
		}
		completed, err := parseCompleted(t.CompletedDate)
		if err != nil {
			continue
		}
		if bestCompleted.IsZero() || completed.After(bestCompleted) {
			best, bestCompleted = exp, completed
		}
	}
	if !best.IsZero() {
		return best, true
	}
	if d, err := time.Parse("2006-01-02", v.MotTestDueDate); err == nil {
		return d, true
	}
	return time.Time{}, false
}

// parseCompleted accepts the two shapes DVSA has used for completedDate.
func parseCompleted(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006.01.02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("dvsa: unrecognised completedDate %q", s)
}

// Item converts the response into the MOT register item.
func (v *VehicleResponse) Item(subjectID, subjectName string) fleet.Item {
	it := fleet.Item{
		SubjectType: fleet.SubjectVehicle,
		SubjectID:   subjectID,
		SubjectName: subjectName,
		Kind:        fleet.KindMOT,
		Source:      fleet.SourceDVSA,
	}
	if d, ok := v.MOTExpiry(); ok {
		it.Expires = d
	} else {
		it.Note = "no passed MOT test on record"
	}
	if len(v.MotTests) == 0 && v.MotTestDueDate != "" {
		it.Note = "first MOT due, never tested"
	}
	return it
}
