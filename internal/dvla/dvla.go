// Package dvla is a client for the DVLA Vehicle Enquiry Service (VES) API,
// which supplies vehicle tax due date and MOT status for GB vehicles.
//
// A free API key is required. Nothing in this package invents data: with no
// key configured, New returns ErrNoCredentials and the caller falls back to
// whatever the operator typed into the CSV.
package dvla

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/moveeeax/fleet-expiry-watch/internal/fleet"
)

// DefaultBaseURL is the live VES endpoint. The UAT endpoint is a different host
// and is set through FLEETWATCH_DVLA_BASE_URL when testing against it.
const DefaultBaseURL = "https://driver-vehicle-licensing.api.gov.uk/vehicle-enquiry/v1"

// ErrNoCredentials means no API key was configured.
var ErrNoCredentials = errors.New("dvla: no API key configured (set FLEETWATCH_DVLA_API_KEY)")

// ErrNotFound means DVLA holds no record for that registration.
var ErrNotFound = errors.New("dvla: vehicle not found")

// Client talks to the VES API.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// New builds a client from the environment. The API key is only ever read from
// the environment; it is never written to disk or into a report.
func New() (*Client, error) {
	key := strings.TrimSpace(os.Getenv("FLEETWATCH_DVLA_API_KEY"))
	if key == "" {
		return nil, ErrNoCredentials
	}
	base := strings.TrimSpace(os.Getenv("FLEETWATCH_DVLA_BASE_URL"))
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimSuffix(base, "/"),
		APIKey:  key,
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}, nil
}

// VehicleResponse is the subset of the VES payload this tool uses.
type VehicleResponse struct {
	RegistrationNumber string `json:"registrationNumber"`
	TaxStatus          string `json:"taxStatus"`
	TaxDueDate         string `json:"taxDueDate"`
	MotStatus          string `json:"motStatus"`
	MotExpiryDate      string `json:"motExpiryDate"`
	Make               string `json:"make"`
	YearOfManufacture  int    `json:"yearOfManufacture"`
	Wheelplan          string `json:"wheelplan"`
}

type errorResponse struct {
	Errors []struct {
		Status string `json:"status"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"errors"`
}

// Lookup fetches one vehicle by normalised registration mark.
func (c *Client) Lookup(ctx context.Context, plate string) (*VehicleResponse, error) {
	body, err := json.Marshal(map[string]string{"registrationNumber": plate})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/vehicles", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dvla: %s: %w", plate, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var v VehicleResponse
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("dvla: %s: decoding response: %w", plate, err)
		}
		return &v, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", plate, ErrNotFound)
	default:
		var e errorResponse
		if json.Unmarshal(raw, &e) == nil && len(e.Errors) > 0 {
			return nil, fmt.Errorf("dvla: %s: %s (%d): %s",
				plate, e.Errors[0].Title, resp.StatusCode, e.Errors[0].Detail)
		}
		return nil, fmt.Errorf("dvla: %s: unexpected status %d", plate, resp.StatusCode)
	}
}

// Items converts a VES response into dated register items. Only dates DVLA
// actually returned produce a date; anything absent stays unrecorded rather
// than being guessed at.
func (v *VehicleResponse) Items(subjectID, subjectName string) []fleet.Item {
	items := []fleet.Item{
		newItem(subjectID, subjectName, fleet.KindTax, v.TaxDueDate, "tax status: "+lower(v.TaxStatus)),
	}
	if v.MotExpiryDate != "" || v.MotStatus != "" {
		items = append(items, newItem(subjectID, subjectName, fleet.KindMOT,
			v.MotExpiryDate, "DVLA MOT status: "+lower(v.MotStatus)))
	}
	return items
}

func newItem(id, name string, kind fleet.Kind, date, note string) fleet.Item {
	it := fleet.Item{
		SubjectType: fleet.SubjectVehicle,
		SubjectID:   id,
		SubjectName: name,
		Kind:        kind,
		Source:      fleet.SourceDVLA,
		Note:        note,
	}
	if d, err := time.Parse("2006-01-02", date); err == nil {
		it.Expires = d
	}
	return it
}

func lower(s string) string {
	if s == "" {
		return "unknown"
	}
	return strings.ToLower(s)
}
