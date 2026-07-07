package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCommandsTableSnapshots(t *testing.T) {
	server := newReadFixtureServer(t, false)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_read", configFileMode)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "invoice list",
			args: []string{"invoice", "list", "--status", "sent", "--search", "contoso", "--limit", "1", "--cursor", "0"},
			want: strings.Join([]string{
				"NUMBER       CLIENT        ISSUED                AMOUNT      RATE                  GBP APPROX  STATUS",
				"INV-2026-01  Contoso GmbH  2026-04-01T00:00:00Z  450000 EUR  0.850000000000000000  382500 GBP  sent",
				"Showing 1 of 2",
				"Totals: 450000 EUR approx 382500 GBP",
				"Next cursor: 1",
				"",
			}, "\n"),
		},
		{
			name: "invoice show by number",
			args: []string{"invoice", "show", "INV-2026-01"},
			want: strings.Join([]string{
				"ID             inv_1",
				"NUMBER         INV-2026-01",
				"CLIENT ID      client_contoso",
				"STATUS         sent",
				"CURRENCY       EUR",
				"ISSUE DATE     2026-04-01T00:00:00Z",
				"DUE DATE       2026-04-15T00:00:00Z",
				"VAT TREATMENT  reverse-charge-eu-b2b",
				"",
				"DESCRIPTION       QTY  UNIT        LINE TOTAL",
				"Monthly retainer  1    450000 EUR  450000 EUR",
				"",
				"SUBTOTAL    450000 EUR",
				"VAT         0 EUR",
				"TOTAL       450000 EUR",
				"GBP APPROX  382500 GBP",
				"",
				"SETTLED DATE    -",
				"SETTLED AMOUNT  -",
				"SETTLEMENT TXN  -",
				"",
			}, "\n"),
		},
		{name: "client list", args: []string{"client", "list"}, want: "ID              NAME          EMAIL                CURRENCY  TERMS  VAT                    RETAINER    DAY RATE\nclient_contoso  Contoso GmbH  ops@contoso.example  EUR       14     reverse-charge-eu-b2b  450000 EUR  -\n"},
		{name: "bank accounts", args: []string{"bank", "accounts"}, want: "ID  NAME                  PROVIDER  CURRENCY  LEDGER         UNRECONCILED\n1   Revolut Business EUR  revolut   EUR       1001-bank-eur  2\n"},
		{name: "bank review", args: []string{"bank", "review"}, want: "KIND   TXN  DATE        PAYEE    AMOUNT      CONFIDENCE  EXPLANATION\nmatch  10   2026-04-03  Contoso  450000 EUR  0.98        Exact invoice amount and payee match.\n"},
		{name: "bank feed", args: []string{"bank", "feed", "--account", "1", "--state", "suggested", "--cursor", "bank-cursor"}, want: "ID  ACCOUNT  DATE        PAYEE    REFERENCE    AMOUNT      STATE\n10  1        2026-04-03  Contoso  INV-2026-01  450000 EUR  suggested\nNext cursor: bank-next\n"},
		{name: "dla ledger", args: []string{"dla", "ledger", "--from", "2026-04-01", "--to", "2026-04-30", "--cursor", "dla-cursor"}, want: "DATE        ENTRY            KIND     OWED TO YOU  DRAWN      BALANCE\n2026-04-05  Director travel  drawing  0 GBP        12000 GBP  12000 GBP DR\nNext cursor: dla-next\n"},
		{name: "dla balance", args: []string{"dla", "balance"}, want: "STATUS               overdrawn\nBALANCE              12000 GBP\nSUGGESTED CLEARANCE  12000 GBP\nPOLICY STATUS        Director owes the company\nPOLICY REMEDY        clear with dividend or repayment\nS455 CHARGE          true\n"},
		{name: "dividend headroom", args: []string{"dividend", "headroom"}, want: "LINE                     AMOUNT\nRetained earnings        200000 GBP\nAvailable to distribute  200000 GBP\nFinancial year: 2026-27\nAs of: 2026-04-30T00:00:00Z\nDistributable: true\nAvailable: 200000 GBP\n"},
		{name: "dividend history", args: []string{"dividend", "history"}, want: "ID     DECLARED              SHAREHOLDER  AMOUNT     PER SHARE  SHARES  VOUCHER  MINUTES\ndiv_1  2026-04-30T00:00:00Z  N. Meyer     50000 GBP  500 GBP    100     ready    pending\n"},
		{name: "report pl", args: []string{"report", "pl", "--from", "2026-04-01", "--to", "2026-06-30"}, want: "KIND     LINE                             AMOUNT\nincome   Contoso services                 450000 GBP\nincome   Realised FX gains on settlement  1500 GBP\ntotal    income total                     451500 GBP\nexpense  Software                         10000 GBP\ntotal    expense total                    10000 GBP\ntotal    profit before tax                441500 GBP\ntax      IoM income tax at 0%             0 GBP\ntotal    net profit                       441500 GBP\nPeriod: 2026-04-01 to 2026-06-30\nTax year: 2026-27\n"},
		{name: "report vat", args: []string{"report", "vat", "--period", "2026-Q2"}, want: "BOX   LABEL               AMOUNT\nbox1  VAT due on sales    90000 GBP\nbox4  VAT reclaimed       2000 GBP\nbox6  Total sales ex-VAT  450000 GBP\nnet   Net position        88000 GBP\nPeriod: 2026-04-01 to 2026-06-30\n"},
		{name: "report calendar", args: []string{"report", "calendar"}, want: "KEY         LABEL       AUTHORITY  DUE         DAYS  STATUS\nvat_return  VAT return  IoM C&E    2026-07-31  24    due-soon\n"},
		{name: "report profit-ytd", args: []string{"report", "profit-ytd", "--tax-year", "2026-27"}, want: "TAX YEAR  2026-27\nPROFIT    441500 GBP\n"},
		{name: "advisor insights", args: []string{"advisor", "insights", "--surface", "invoices"}, want: "SEVERITY  TEXT                                       CTA            ACTION\namber     Invoice INV-2026-01 is due for follow-up.  Send reminder  invoicing.sendReminder\n"},
		{name: "rates today", args: []string{"rates", "today", "--from", "EUR", "--to", "GBP"}, want: "FROM        EUR\nTO          GBP\nRATE        0.850000000000000000\nRATE DATE   2026-04-30\nFETCHED AT  2026-04-30T12:00:00Z\nSOURCE      ecb\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			args := append([]string{"--config", configPath}, tt.args...)
			err := Execute(context.Background(), args, &stdout, ioDiscard{})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got := stdout.String(); got != tt.want {
				t.Fatalf("stdout mismatch\n--- got ---\n%s--- want ---\n%s", got, tt.want)
			}
		})
	}
}

func TestReadCommandsJSONPassthrough(t *testing.T) {
	server := newReadFixtureServer(t, false)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_json", configFileMode)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "invoice list", args: []string{"--json", "invoice", "list"}, want: `"invoices": [`},
		{name: "client list", args: []string{"--json", "client", "list"}, want: `"clients": [`},
		{name: "bank accounts", args: []string{"--json", "bank", "accounts"}, want: `"accounts": [`},
		{name: "bank review", args: []string{"--json", "bank", "review"}, want: `"matches": [`},
		{name: "bank feed", args: []string{"--json", "bank", "feed"}, want: `"transactions": [`},
		{name: "dla ledger", args: []string{"--json", "dla", "ledger"}, want: `"next_cursor": "dla-next"`},
		{name: "dla balance", args: []string{"--json", "dla", "balance"}, want: `"suggested_clearance": {`},
		{name: "dividend headroom", args: []string{"--json", "dividend", "headroom"}, want: `"financial_year": "2026-27"`},
		{name: "dividend history", args: []string{"--json", "dividend", "history"}, want: `"declarations": [`},
		{name: "report pl", args: []string{"--json", "report", "pl", "--from", "2026-04-01", "--to", "2026-06-30"}, want: `"net_profit": {`},
		{name: "report vat", args: []string{"--json", "report", "vat", "--period", "2026-Q2"}, want: `"net_position": {`},
		{name: "report calendar", args: []string{"--json", "report", "calendar"}, want: `"filings": [`},
		{name: "report profit-ytd", args: []string{"--json", "report", "profit-ytd", "--tax-year", "2026-27"}, want: `"profit": {`},
		{name: "advisor insights", args: []string{"--json", "advisor", "insights"}, want: `"rendered_text": "Invoice INV-2026-01 is due for follow-up."`},
		{name: "rates today", args: []string{"--json", "rates", "today"}, want: `"rate": "0.850000000000000000"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			args := append([]string{"--config", configPath}, tt.args...)
			err := Execute(context.Background(), args, &stdout, ioDiscard{})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !json.Valid(stdout.Bytes()) {
				t.Fatalf("stdout is not JSON: %s", stdout.String())
			}
			if !strings.Contains(stdout.String(), tt.want) {
				t.Fatalf("stdout = %s, want substring %q", stdout.String(), tt.want)
			}
		})
	}
}

func TestInvoicePDFWritesRedirectBody(t *testing.T) {
	server := newReadFixtureServer(t, false)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_pdf", configFileMode)
	output := filepath.Join(t.TempDir(), "invoice.pdf")

	var stdout bytes.Buffer
	err := Execute(context.Background(), []string{"--config", configPath, "invoice", "pdf", "inv_1", "--output", output}, &stdout, ioDiscard{})
	if err != nil {
		t.Fatalf("invoice pdf error = %v", err)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output PDF: %v", err)
	}
	if got, want := string(body), "%PDF-1.4\nfixture\n%%EOF\n"; got != want {
		t.Fatalf("PDF body = %q, want %q", got, want)
	}
	if !strings.Contains(stdout.String(), "Wrote "+output) {
		t.Fatalf("stdout = %q, want written path", stdout.String())
	}
}

func TestReadCommandUnauthenticatedExits3(t *testing.T) {
	server := newReadFixtureServer(t, true)
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_bad", configFileMode)

	err := Execute(context.Background(), []string{"--config", configPath, "bank", "accounts"}, ioDiscard{}, ioDiscard{})
	if err == nil {
		t.Fatal("bank accounts error = nil, want auth error")
	}
	if exitCode(err) != ExitAuth {
		t.Fatalf("exit code = %d, want %d; err=%v", exitCode(err), ExitAuth, err)
	}
}

func TestReadCommandEmptyStates(t *testing.T) {
	server := newReadFixtureServer(t, false, withEmptyReadFixtures())
	defer server.Close()
	configPath := writeTestConfig(t, server.URL, "lgy_empty", configFileMode)

	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"invoice", "list"}, want: "No invoices match the current filters.\n"},
		{args: []string{"client", "list"}, want: "No clients found.\n"},
		{args: []string{"bank", "accounts"}, want: "No bank accounts found.\n"},
		{args: []string{"bank", "review"}, want: "No banking review cards waiting.\n"},
		{args: []string{"bank", "feed"}, want: "No bank transactions match the current filters.\n"},
		{args: []string{"dla", "ledger"}, want: "No DLA ledger entries match the current filters.\n"},
		{args: []string{"dividend", "history"}, want: "No dividend declarations found.\n"},
		{args: []string{"report", "calendar"}, want: "No report filings found.\n"},
		{args: []string{"advisor", "insights"}, want: "No advisor insights for this surface.\n"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			args := append([]string{"--config", configPath}, tt.args...)
			err := Execute(context.Background(), args, &stdout, ioDiscard{})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got := stdout.String(); got != tt.want {
				t.Fatalf("stdout = %q, want %q", got, tt.want)
			}
		})
	}
}

type readFixtureOptions struct {
	empty bool
}

type readFixtureOption func(*readFixtureOptions)

func withEmptyReadFixtures() readFixtureOption {
	return func(options *readFixtureOptions) {
		options.empty = true
	}
}

func newReadFixtureServer(t *testing.T, unauthenticated bool, opts ...readFixtureOption) *httptest.Server {
	t.Helper()

	options := readFixtureOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files/invoice.pdf" {
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4\nfixture\n%%EOF\n"))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer lgy_read" && got != "Bearer lgy_json" && got != "Bearer lgy_pdf" && got != "Bearer lgy_bad" && got != "Bearer lgy_empty" && got != "Bearer lgy_write" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if unauthenticated {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"type":"https://ledgerly.local/problems/unauthenticated","title":"Unauthorized","status":401,"detail":"authentication required"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if options.empty {
			writeEmptyReadFixture(t, w, r)
			return
		}
		writeReadFixture(t, w, r)
	}))
}

func writeEmptyReadFixture(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	switch r.URL.Path {
	case "/api/identity/me":
		_, _ = w.Write([]byte(`{"id":1,"email":"owner@example.com","name":"Owner","created_at":"2026-07-05T12:00:00Z","token_name":"CLI read integration","token_scope":"read-only"}`))
	case "/api/invoicing/invoices":
		_, _ = w.Write([]byte(`{"invoices":[],"counts":[],"total_count":0,"limit":50,"offset":0,"totals":{"subtotals":[],"total_gbp":{"amount":0,"currency":"GBP"}}}`))
	case "/api/invoicing/clients":
		_, _ = w.Write([]byte(`{"clients":[]}`))
	case "/api/banking/accounts":
		_, _ = w.Write([]byte(`{"accounts":[]}`))
	case "/api/banking/review":
		_, _ = w.Write([]byte(`{"matches":[],"rules":[],"suggestions":[]}`))
	case "/api/banking/feed":
		_, _ = w.Write([]byte(`{"transactions":[],"next_cursor":null}`))
	case "/api/dla/ledger":
		_, _ = w.Write([]byte(`{"entries":[],"next_cursor":null}`))
	case "/api/dividends/history":
		_, _ = w.Write([]byte(`{"declarations":[]}`))
	case "/api/reports/calendar":
		_, _ = w.Write([]byte(`{"filings":[]}`))
	case "/api/advisor/insights":
		_, _ = w.Write([]byte(`{"insights":[]}`))
	default:
		writeReadFixture(t, w, r)
	}
}

func writeReadFixture(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	switch r.URL.Path {
	case "/api/identity/me":
		_, _ = w.Write([]byte(`{"id":1,"email":"owner@example.com","name":"Owner","created_at":"2026-07-05T12:00:00Z","token_name":"CLI read integration","token_scope":"read-only"}`))
	case "/api/invoicing/invoices":
		if r.URL.Query().Get("status") == "sent" {
			assertQuery(t, r, map[string]string{
				"status": "sent",
				"search": "contoso",
				"limit":  "1",
				"offset": "0",
			}, false)
		}
		_, _ = w.Write([]byte(invoiceListFixture))
	case "/api/invoicing/invoices/INV-2026-01":
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not found","status":404,"detail":"missing invoice"}`))
	case "/api/invoicing/invoices/inv_1":
		_, _ = w.Write([]byte(invoiceDetailFixture))
	case "/api/invoicing/invoices/inv_1/pdf":
		http.Redirect(w, r, "/files/invoice.pdf", http.StatusFound)
	case "/api/invoicing/clients":
		_, _ = w.Write([]byte(clientListFixture))
	case "/api/banking/accounts":
		_, _ = w.Write([]byte(bankAccountsFixture))
	case "/api/banking/review":
		_, _ = w.Write([]byte(bankReviewFixture))
	case "/api/banking/feed":
		_, _ = w.Write([]byte(bankFeedFixture))
	case "/api/dla/ledger":
		_, _ = w.Write([]byte(dlaLedgerFixture))
	case "/api/dla/balance":
		_, _ = w.Write([]byte(dlaBalanceFixture))
	case "/api/dividends/headroom":
		_, _ = w.Write([]byte(dividendHeadroomFixture))
	case "/api/dividends/history":
		_, _ = w.Write([]byte(dividendHistoryFixture))
	case "/api/reports/pl":
		_, _ = w.Write([]byte(reportPLFixture))
	case "/api/reports/vat":
		_, _ = w.Write([]byte(reportVATFixture))
	case "/api/reports/calendar":
		_, _ = w.Write([]byte(reportCalendarFixture))
	case "/api/reports/profit-ytd":
		_, _ = w.Write([]byte(reportProfitYTDFixture))
	case "/api/advisor/insights":
		_, _ = w.Write([]byte(advisorInsightsFixture))
	case "/api/moneyfx/rates/today":
		_, _ = w.Write([]byte(ratesTodayFixture))
	default:
		t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
	}
}

func assertQuery(t *testing.T, r *http.Request, want map[string]string, strict bool) {
	t.Helper()
	query := r.URL.Query()
	for key, value := range want {
		if got := query.Get(key); got != value {
			t.Fatalf("%s query %s = %q, want %q", r.URL.Path, key, got, value)
		}
	}
	if strict && len(query) != len(want) {
		t.Fatalf("%s query = %v, want exactly %v", r.URL.Path, query, want)
	}
}

const invoiceListFixture = `{
  "invoices": [{
    "id": "inv_1",
    "client_id": "client_contoso",
    "client_name": "Contoso GmbH",
    "number": "INV-2026-01",
    "status": "sent",
    "currency": "EUR",
    "issue_date": "2026-04-01T00:00:00Z",
    "due_date": "2026-04-15T00:00:00Z",
    "days_overdue": 0,
    "created_at": "2026-04-01T00:00:00Z",
    "updated_at": "2026-04-01T00:00:00Z",
    "totals": {
      "subtotal": {"amount": 450000, "currency": "EUR"},
      "vat": {"amount": 0, "currency": "EUR"},
      "total": {"amount": 450000, "currency": "EUR"},
      "approx_gbp": {"amount": {"amount": 382500, "currency": "GBP"}, "as_of": "2026-04-01T00:00:00Z", "locked": true, "rate": {"from": "EUR", "to": "GBP", "value": "0.850000000000000000", "rate_date": "2026-04-01T00:00:00Z", "source": "ecb"}}
    }
  }],
  "counts": [{"status": "sent", "count": 2}],
  "total_count": 2,
  "limit": 1,
  "offset": 0,
  "totals": {"subtotals": [{"amount": 450000, "currency": "EUR"}], "total_gbp": {"amount": 382500, "currency": "GBP"}}
}`

const invoiceDetailFixture = `{
  "id": "inv_1",
  "client_id": "client_contoso",
  "number": "INV-2026-01",
  "status": "sent",
  "currency": "EUR",
  "issue_date": "2026-04-01T00:00:00Z",
  "due_date": "2026-04-15T00:00:00Z",
  "vat_treatment": "reverse-charge-eu-b2b",
  "lock_id": "lock_1",
  "pdf_asset": "/files/invoice.pdf",
  "sent_at": "2026-04-01T12:00:00Z",
  "settled_amount": null,
  "settled_date": null,
  "settlement_txn_ref": null,
  "created_at": "2026-04-01T00:00:00Z",
  "updated_at": "2026-04-01T00:00:00Z",
  "lines": [{"id": "line_1", "invoice_id": "inv_1", "description": "Monthly retainer", "qty": "1", "unit_price": {"amount": 450000, "currency": "EUR"}, "line_total": {"amount": 450000, "currency": "EUR"}, "position": 1}],
  "totals": {
    "subtotal": {"amount": 450000, "currency": "EUR"},
    "vat": {"amount": 0, "currency": "EUR"},
    "total": {"amount": 450000, "currency": "EUR"},
    "approx_gbp": {"amount": {"amount": 382500, "currency": "GBP"}, "as_of": "2026-04-01T00:00:00Z", "locked": true, "rate": {"from": "EUR", "to": "GBP", "value": "0.850000000000000000", "rate_date": "2026-04-01T00:00:00Z", "source": "ecb"}}
  }
}`

const clientListFixture = `{"clients":[{"id":"client_contoso","name":"Contoso GmbH","email":"ops@contoso.example","default_currency":"EUR","terms_days":14,"vat_treatment":"reverse-charge-eu-b2b","vat_number":"DE129273398","retainer_amount":{"amount_minor":450000,"currency":"EUR"},"day_rate":null,"archived_at":null,"created_at":"2026-04-01T00:00:00Z","address":{"line1":"1 Test Strasse","line2":"","locality":"Berlin","region":"","postal_code":"10115","country":"DE"}}]}`

const bankAccountsFixture = `{"accounts":[{"id":1,"name":"Revolut Business EUR","provider":"revolut","currency":"EUR","ledger_account_code":"1001-bank-eur","unreconciled_count":2,"created_at":"2026-04-01T00:00:00Z"}]}`

const bankReviewFixture = `{"matches":[{"kind":"match","suggestion_id":100,"confidence":0.98,"explanation":"Exact invoice amount and payee match.","target":{"type":"invoice","id":"inv_1","invoice_number":"INV-2026-01","client":"Contoso GmbH","times_applied":null},"transaction":{"id":10,"account_id":1,"import_batch_id":7,"date":"2026-04-03","payee":"Contoso","reference":"INV-2026-01","amount":{"amount_minor":450000,"currency":"EUR"},"state":"suggested","provider_meta":{},"created_at":"2026-04-03T00:00:00Z"}}],"rules":[],"suggestions":[]}`

const bankFeedFixture = `{"transactions":[{"id":10,"account_id":1,"import_batch_id":7,"date":"2026-04-03","payee":"Contoso","reference":"INV-2026-01","amount":{"amount_minor":450000,"currency":"EUR"},"state":"suggested","provider_meta":{},"created_at":"2026-04-03T00:00:00Z"}],"next_cursor":"bank-next"}`

const dlaLedgerFixture = `{"entries":[{"id":1,"date":"2026-04-05","description":"Director travel","kind":"drawing","amount":{"amount_minor":12000,"currency":"GBP"},"owed_to_you":{"amount_minor":0,"currency":"GBP"},"drawn":{"amount_minor":12000,"currency":"GBP"},"running_balance":{"amount_minor":12000,"currency":"GBP"},"balance_side":"DR","source_ref":"manual:1","created_at":"2026-04-05T00:00:00Z"}],"next_cursor":"dla-next"}`

const dlaBalanceFixture = `{"balance":{"amount_minor":12000,"currency":"GBP"},"status":"overdrawn","suggested_clearance":{"amount_minor":12000,"currency":"GBP"},"policy":{"credit_status_text":"Director owes the company","credit_explainer_template":"Company owes you {{balance}}.","overdrawn_warning_template":"Clear {{balance}}.","bik_warning_key":"dla.overdrawn","remedy":"clear with dividend or repayment","s455_charge":true}}`

const dividendHeadroomFixture = `{"as_of":"2026-04-30T00:00:00Z","financial_year":"2026-27","distributable":true,"available":{"amount":200000,"currency":"GBP"},"lines":[{"label":"Retained earnings","amount":{"amount":200000,"currency":"GBP"}},{"label":"Available to distribute","amount":{"amount":200000,"currency":"GBP"}}]}`

const dividendHistoryFixture = `{"declarations":[{"id":"div_1","declared_date":"2026-04-30T00:00:00Z","created_at":"2026-04-30T00:00:00Z","shareholder_name":"N. Meyer","shares":100,"amount":{"amount":50000,"currency":"GBP"},"per_share":{"amount":500,"currency":"GBP"},"voucher_asset":"/files/voucher.pdf","minutes_asset":null,"company_snapshot":null,"shareholder_snapshot":null,"headroom_snapshot":null,"withholding_snapshot":null}]}`

const reportPLFixture = `{"period":{"from":"2026-04-01","to":"2026-06-30"},"tax_year":"2026-27","income":[{"label":"Contoso services","client_id":"client_contoso","client_name":"Contoso GmbH","currency":"EUR","amount":{"amount_minor":450000,"currency":"GBP"}}],"income_total":{"amount_minor":451500,"currency":"GBP"},"realised_fx_gains":{"label":"Realised FX gains on settlement","amount":{"amount_minor":1500,"currency":"GBP"}},"expenses":[{"account_code":"5010-software","account_name":"Software","amount":{"amount_minor":10000,"currency":"GBP"}}],"expense_total":{"amount_minor":10000,"currency":"GBP"},"profit_before_tax":{"amount_minor":441500,"currency":"GBP"},"corporate_tax":{"label":"IoM income tax at 0%","rate":"0","tax_year":"2026-27","amount":{"amount_minor":0,"currency":"GBP"}},"net_profit":{"amount_minor":441500,"currency":"GBP"}}`

const reportVATFixture = `{"period":{"from":"2026-04-01","to":"2026-06-30"},"status":"registered","box1":{"amount_minor":90000,"currency":"GBP"},"box4":{"amount_minor":2000,"currency":"GBP"},"box6":{"amount_minor":450000,"currency":"GBP"},"net_position":{"amount_minor":88000,"currency":"GBP"}}`

const reportCalendarFixture = `{"filings":[{"key":"vat_return","label":"VAT return","authority":"IoM C&E","due_date":"2026-07-31","days_until":24,"status":"due-soon"}]}`

const reportProfitYTDFixture = `{"tax_year":"2026-27","profit":{"amount_minor":441500,"currency":"GBP"}}`

const advisorInsightsFixture = `{"insights":[{"key":"invoice.overdue","rule_id":"invoice.overdue","severity":"amber","rendered_text":"Invoice INV-2026-01 is due for follow-up.","surfaces":["invoices"],"bindings":{},"cta":{"label":"Send reminder","action":"invoicing.sendReminder","params":{"invoice_id":"inv_1"}},"created_at":"2026-04-30T00:00:00Z"}]}`

const ratesTodayFixture = `{"from":"EUR","to":"GBP","rate":"0.850000000000000000","rate_date":"2026-04-30","fetched_at":"2026-04-30T12:00:00Z","source":"ecb"}`
