//go:build integration

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/npmulder/ledgerly/internal/identity"
	"github.com/npmulder/ledgerly/internal/it/harness"
	"github.com/npmulder/ledgerly/internal/platform/bus"
	"github.com/npmulder/ledgerly/internal/platform/db"
	httpserver "github.com/npmulder/ledgerly/internal/platform/http"
)

func TestIdentityRegisterWithProfileFirstRunFlow(t *testing.T) {
	h := harness.New(t, harness.Options{ClockStart: time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)})
	resetIdentityFirstRunState(t, h)

	profileUpdated := make(chan identity.ProfileUpdated, 1)
	h.Bus.Subscribe(identity.ProfileUpdatedEventName, func(_ context.Context, _ db.Tx, event bus.Event) error {
		updated, ok := event.(identity.ProfileUpdated)
		if !ok {
			return fmt.Errorf("event type = %T, want identity.ProfileUpdated", event)
		}
		profileUpdated <- updated
		return nil
	})

	body, cookies, status, contentType := postHarnessJSON(t, h, "/api/identity/register-with-profile", identityFirstRunPayload())
	if status != nethttp.StatusCreated {
		t.Fatalf("register-with-profile status = %d, want %d; body=%s", status, nethttp.StatusCreated, string(body))
	}
	if contentType != "application/json" {
		t.Fatalf("register-with-profile Content-Type = %q, want application/json", contentType)
	}
	if !hasSessionCookie(cookies) {
		t.Fatalf("register-with-profile cookies = %+v, want %s", cookies, identity.SessionCookieName)
	}

	var response struct {
		User struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
		Profile struct {
			TradingName   string `json:"trading_name"`
			LegalName     string `json:"legal_name"`
			CompanyNumber string `json:"company_number"`
			YearEnd       struct {
				Month int `json:"month"`
				Day   int `json:"day"`
			} `json:"year_end"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode register-with-profile response: %v; body=%s", err, string(body))
	}
	if response.User.Email != "owner@example.test" || response.User.Name != "Owner" {
		t.Fatalf("response user = %+v, want owner@example.test Owner", response.User)
	}
	if response.Profile.TradingName != "Acme Trading" || response.Profile.LegalName != "Acme Limited" || response.Profile.CompanyNumber != "ACME123" {
		t.Fatalf("response profile = %+v, want Acme profile", response.Profile)
	}
	if response.Profile.YearEnd.Month != 12 || response.Profile.YearEnd.Day != 31 {
		t.Fatalf("response year_end = %+v, want 31 December", response.Profile.YearEnd)
	}

	select {
	case <-profileUpdated:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for identity.ProfileUpdated event")
	}
	if err := h.WaitAdvisorIdle(); err != nil {
		t.Fatalf("WaitAdvisorIdle() after profile update error = %v", err)
	}
	assertIdentityFirstRunRows(t, h)

	profileBody, profileStatus, _ := getHarnessJSON(t, h, "/api/identity/profile")
	if profileStatus != nethttp.StatusOK {
		t.Fatalf("GET profile after first-run status = %d, want %d; body=%s", profileStatus, nethttp.StatusOK, string(profileBody))
	}

	closedBody, _, closedStatus, closedContentType := postHarnessJSON(t, h, "/api/identity/register-with-profile", identityFirstRunPayload())
	if closedStatus != nethttp.StatusForbidden {
		t.Fatalf("second register-with-profile status = %d, want %d; body=%s", closedStatus, nethttp.StatusForbidden, string(closedBody))
	}
	if closedContentType != httpserver.ProblemContentType {
		t.Fatalf("second register-with-profile Content-Type = %q, want %s", closedContentType, httpserver.ProblemContentType)
	}
}

func resetIdentityFirstRunState(t *testing.T, h *harness.Harness) {
	t.Helper()

	h.Tx(func(ctx context.Context, tx db.Tx) error {
		_, err := tx.Exec(ctx, `
TRUNCATE identity.sessions,
	identity.pats,
	identity.users,
	identity.company_profile
RESTART IDENTITY`)
		return err
	})
}

func assertIdentityFirstRunRows(t *testing.T, h *harness.Harness) {
	t.Helper()

	h.Tx(func(ctx context.Context, tx db.Tx) error {
		for table, want := range map[string]int{
			"identity.users":           1,
			"identity.sessions":        1,
			"identity.company_profile": 1,
		} {
			var got int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("%s rows = %d, want %d", table, got, want)
			}
		}
		return nil
	})
}

func postHarnessJSON(t *testing.T, h *harness.Harness, path string, payload any) ([]byte, []*nethttp.Cookie, int, string) {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s request: %v", path, err)
	}
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create POST %s request: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST %s response: %v", path, err)
	}
	return responseBody, resp.Cookies(), resp.StatusCode, resp.Header.Get("Content-Type")
}

func getHarnessJSON(t *testing.T, h *harness.Harness, path string) ([]byte, int, string) {
	t.Helper()

	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("create GET %s request: %v", path, err)
	}
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s response: %v", path, err)
	}
	return body, resp.StatusCode, resp.Header.Get("Content-Type")
}

func hasSessionCookie(cookies []*nethttp.Cookie) bool {
	for _, cookie := range cookies {
		if cookie.Name == identity.SessionCookieName && cookie.Value != "" {
			return true
		}
	}
	return false
}

func identityFirstRunPayload() map[string]any {
	return map[string]any{
		"email":              "owner@example.test",
		"password":           "correct horse battery staple",
		"name":               "Owner",
		"trading_name":       "Acme Trading",
		"legal_name":         "Acme Limited",
		"company_number":     "ACME123",
		"incorporation_date": "2024-01-15",
		"year_end_month":     12,
		"year_end_day":       31,
		"registered_office": map[string]any{
			"line1":       "1 Athol Street",
			"line2":       "",
			"locality":    "Douglas",
			"region":      "",
			"postal_code": "",
			"country":     "IM",
		},
	}
}
