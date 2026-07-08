import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "@/app/App";

const originalLocalStorage = Object.getOwnPropertyDescriptor(
  window,
  "localStorage",
);
const dividendSnapshotLogoDataURI =
  "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciLz4=";

afterEach(() => {
  cleanup();
  if (originalLocalStorage) {
    Object.defineProperty(window, "localStorage", originalLocalStorage);
  }
  vi.unstubAllGlobals();
});

const topLevelRoutes = [
  { activeNav: "Dashboard", path: "/", title: "Dashboard" },
  { activeNav: "Invoices", path: "/invoices", title: "Invoices" },
  {
    activeNav: "Invoices",
    path: "/invoices/INV-2026-07",
    title: "Invoice editor",
  },
  { activeNav: "Banking", path: "/banking", title: "Banking" },
  { activeNav: "DLA", path: "/dla", title: "Director's loan · N. Meyer" },
  { activeNav: "Dividends", path: "/dividends", title: "Dividends" },
  { activeNav: "Reports", path: "/reports", title: "Reports" },
  { activeNav: "Settings", path: "/settings", title: "Company" },
] as const;

const settingsRoutes = [
  { path: "/settings/company", title: "Company" },
  { path: "/settings/jurisdiction", title: "Jurisdiction" },
  { path: "/settings/clients", title: "Clients" },
  { path: "/settings/invoicing-defaults", title: "Invoicing defaults" },
  { path: "/settings/bank-connections", title: "Bank connections" },
  { path: "/settings/users", title: "Users" },
] as const;

function renderAt(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  vi.stubGlobal("fetch", authenticatedFetch());

  render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function renderAtUnauthenticated(path: string) {
  renderAtWithFetch(path, unauthenticatedFetch());
}

function renderAtWithFetch(path: string, fetchImpl: typeof fetch) {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  vi.stubGlobal("fetch", fetchImpl);

  render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("App routing shell", () => {
  it.each(topLevelRoutes)(
    "renders shell, page title, and active nav for $path",
    async ({ activeNav, path, title }) => {
      renderAt(path);

      expect(
        await screen.findByRole("heading", { level: 1, name: title }),
      ).toBeInTheDocument();
      expect(screen.getByRole("banner")).toBeInTheDocument();
      expect(screen.getByText("NPM Limited")).toBeInTheDocument();

      const primaryNav = screen.getByRole("navigation", { name: "Primary" });
      expect(
        within(primaryNav).getByRole("link", { name: activeNav }),
      ).toHaveClass("app-shell-nav__link--active");
    },
  );

  it.each(settingsRoutes)(
    "renders settings sub-shell and active settings link for $path",
    async ({ path, title }) => {
      renderAt(path);

      expect(
        await screen.findByRole("heading", { level: 1, name: title }),
      ).toBeInTheDocument();

      expect(
        screen.getByRole("navigation", { name: "Primary" }),
      ).toBeInTheDocument();
      const settingsNav = screen.getByRole("navigation", {
        name: "Settings",
      });
      expect(
        within(settingsNav).getByRole("link", { name: title }),
      ).toHaveClass("settings-shell__link--active");
      expect(screen.getByRole("link", { name: "Settings" })).toHaveClass(
        "app-shell-nav__link--active",
      );
    },
  );

  it("redirects protected routes to login when the session is missing", async () => {
    renderAtUnauthenticated("/settings/company");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Login" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("banner")).not.toBeInTheDocument();
  });

  it("renders registration outside the authenticated shell", () => {
    const fetchImpl = vi.fn(async () => jsonResponse({}, 404));

    renderAtWithFetch("/register", fetchImpl);

    expect(
      screen.getByRole("heading", { level: 1, name: "Set up Ledgerly" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("banner")).not.toBeInTheDocument();
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("renders a shell 404 for unknown app routes", async () => {
    renderAt("/missing-screen");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Page not found",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("banner")).toBeInTheDocument();
  });

  it("keeps print routes outside the app shell", async () => {
    installInvoicePrintPayload("INV-2026-07", printPayload());

    renderAt("/print/invoice/INV-2026-07");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "INV-2026-07",
      }),
    ).toBeInTheDocument();
    expect(screen.getByText("Generated by Ledgerly")).toBeInTheDocument();
    expect(screen.getByText("0.8500")).toBeInTheDocument();
    expect(
      document.querySelector(".app-shell__header"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("navigation", { name: "Primary" }),
    ).not.toBeInTheDocument();
  });

  it("omits invoice print VAT artifacts when the company is not VAT registered", async () => {
    installInvoicePrintPayload(
      "INV-2026-08",
      unregisteredDomesticPrintPayload(),
    );

    renderAt("/print/invoice/INV-2026-08");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "INV-2026-08",
      }),
    ).toBeInTheDocument();
    expect(screen.queryByText("20% VAT")).not.toBeInTheDocument();
    expect(screen.queryByText("VAT no. GB123456789")).not.toBeInTheDocument();
    expect(screen.getAllByText("€1,000.00").length).toBeGreaterThan(0);
  });

  it("renders dividend voucher print routes outside the app shell", async () => {
    installDividendPrintPayload(
      "dividend-2026-07",
      "dividend-voucher",
      dividendDocumentPayload(),
    );

    renderAt("/print/dividend-voucher/dividend-2026-07");

    expect(await screen.findByText("Dividend voucher")).toBeInTheDocument();
    expect(screen.getByText("Company no. 137792C")).toBeInTheDocument();
    expect(screen.getByText("£30.00")).toBeInTheDocument();
    expect(screen.getByText("£3,000.00")).toBeInTheDocument();
    expect(screen.getByText(/withholding: none/)).toBeInTheDocument();
    expect(
      document.querySelector(".dividend-print__brand img"),
    ).toHaveAttribute("src", dividendSnapshotLogoDataURI);
    expect(
      document.querySelector(".app-shell__header"),
    ).not.toBeInTheDocument();
  });

  it("renders board minutes print routes outside the app shell", async () => {
    installDividendPrintPayload(
      "dividend-2026-07",
      "board-minutes",
      dividendDocumentPayload(),
    );

    renderAt("/print/board-minutes/dividend-2026-07");

    expect(await screen.findByText("Board minutes")).toBeInTheDocument();
    expect(screen.getByText("N. Meyer (Director)")).toBeInTheDocument();
    expect(screen.getByText("Available to distribute")).toBeInTheDocument();
    expect(screen.getByText(/director's loan account/)).toBeInTheDocument();
    expect(
      document.querySelector(".app-shell__header"),
    ).not.toBeInTheDocument();
  });

  it("keeps the session visible when logout fails", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const path = pathFromRequest(input);
      if (path === "/api/identity/me") {
        return jsonResponse({
          created_at: "2026-07-05T12:00:00Z",
          email: "owner@example.com",
          id: 1,
          name: "N. Meyer",
        });
      }
      if (path === "/api/identity/profile") {
        return jsonResponse(identityProfile());
      }
      if (path === "/api/dashboard/summary") {
        return jsonResponse(dashboardSummary());
      }
      if (path === "/api/invoicing/clients") {
        return jsonResponse({ clients: [] });
      }
      if (path === "/api/identity/logout") {
        return jsonResponse(
          {
            detail: "session cookie was not cleared",
            status: 503,
            title: "Service unavailable",
            type: "about:blank",
          },
          503,
          "application/problem+json",
        );
      }
      return jsonResponse(
        {
          status: 404,
          title: "Not Found",
          type: "about:blank",
        },
        404,
        "application/problem+json",
      );
    });
    renderAtWithFetch("/", fetchImpl);

    expect(
      await screen.findByRole("heading", { level: 1, name: "Dashboard" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open account menu" }));
    await user.click(screen.getByRole("menuitem", { name: "Logout" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Service unavailable",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "session cookie was not cleared",
    );
    expect(screen.getByRole("banner")).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { level: 1, name: "Login" }),
    ).not.toBeInTheDocument();
  });
});

function authenticatedFetch() {
  return vi.fn(async (input: RequestInfo | URL) => {
    const path = pathFromRequest(input);
    if (path === "/api/identity/me") {
      return jsonResponse({
        created_at: "2026-07-05T12:00:00Z",
        email: "owner@example.com",
        id: 1,
        name: "N. Meyer",
      });
    }
    if (path === "/api/identity/profile") {
      return jsonResponse(identityProfile());
    }
    if (path === "/api/dashboard/summary") {
      return jsonResponse(dashboardSummary());
    }
    if (path === "/api/invoicing/clients") {
      return jsonResponse({ clients: [] });
    }
    if (path === "/api/dla/balance") {
      return jsonResponse(dlaBalance());
    }
    if (path === "/api/dla/ledger") {
      return jsonResponse({ entries: [], next_cursor: null });
    }
    return jsonResponse(
      {
        status: 404,
        title: "Not Found",
        type: "about:blank",
      },
      404,
      "application/problem+json",
    );
  });
}

function printPayload() {
  return {
    client: {
      address: {
        country: "Isle of Man",
        line1: "1 Client Street",
        line2: "",
        locality: "Douglas",
        postal_code: "IM1 1AA",
        region: "",
      },
      archived_at: null,
      created_at: "2026-07-01T00:00:00Z",
      default_currency: "EUR",
      id: "client-1",
      name: "Contoso BV",
      terms_days: 30,
      vat_number: "NL123456789B01",
      vat_treatment: "reverse-charge-eu-b2b",
    },
    draft_watermark: false,
    identity: {
      address: {
        country: "Isle of Man",
        line1: "Exchange House",
        line2: "54 Athol Street",
        locality: "Douglas",
        postal_code: "IM1 1JD",
        region: "",
      },
      bank_name: "Isle of Man Bank",
      bic: "RBOSIMD2",
      company_number: "012345V",
      iban: "GB29NWBK60161331926819",
      legal_name: "Ledgerly Consulting Limited",
      trading_name: "Ledgerly Consulting",
      vat_number: "GB123456789",
    },
    invoice: {
      client_id: "client-1",
      created_at: "2026-07-01T00:00:00Z",
      currency: "EUR",
      due_date: "2026-07-31T00:00:00Z",
      id: "INV-2026-07",
      issue_date: "2026-07-01T00:00:00Z",
      lines: [
        {
          description: "Consulting services",
          id: "line-1",
          invoice_id: "INV-2026-07",
          line_total: { amount: 420000, currency: "EUR" },
          position: 1,
          qty: "1",
          unit_price: { amount: 420000, currency: "EUR" },
        },
      ],
      lock_id: "42",
      number: "INV-2026-07",
      pdf_asset: null,
      sent_at: "2026-07-01T12:00:00Z",
      settled_amount: null,
      settled_date: null,
      settlement_txn_ref: null,
      status: "sent",
      totals: {
        subtotal: { amount: 420000, currency: "EUR" },
        total: { amount: 420000, currency: "EUR" },
        vat: { amount: 0, currency: "EUR" },
      },
      updated_at: "2026-07-01T12:00:00Z",
      vat_treatment: "reverse-charge-eu-b2b",
    },
    locked_rate: { id: 42, rate: "0.850000000000000000" },
    reverse_charge_note:
      "Reverse charge: customer to account for VAT under Article 196 of Council Directive 2006/112/EC.",
    vat_registered: true,
    vat_rate: "0.2",
    vat_tax_year: "2026",
  };
}

function unregisteredDomesticPrintPayload() {
  const payload = printPayload();
  return {
    ...payload,
    client: {
      ...payload.client,
      name: "Fabrikam Limited",
      vat_number: null,
      vat_treatment: "domestic",
    },
    identity: {
      ...payload.identity,
      vat_number: "GB123456789",
    },
    invoice: {
      ...payload.invoice,
      client_id: "client-1",
      currency: "EUR",
      id: "INV-2026-08",
      lines: [
        {
          description: "Domestic consulting services",
          id: "line-1",
          invoice_id: "INV-2026-08",
          line_total: { amount: 100000, currency: "EUR" },
          position: 1,
          qty: "1",
          unit_price: { amount: 100000, currency: "EUR" },
        },
      ],
      number: "INV-2026-08",
      totals: {
        subtotal: { amount: 100000, currency: "EUR" },
        total: { amount: 100000, currency: "EUR" },
        vat: { amount: 0, currency: "EUR" },
      },
      vat_treatment: "domestic",
    },
    locked_rate: null,
    reverse_charge_note: null,
    vat_registered: false,
  };
}

function installInvoicePrintPayload(invoiceID: string, payload: unknown) {
  installStoredPrintPayload(`ledgerly.print.invoice.${invoiceID}`, payload);
}

function installDividendPrintPayload(
  declarationID: string,
  kind: "board-minutes" | "dividend-voucher",
  payload: unknown,
) {
  installStoredPrintPayload(
    `ledgerly.print.dividend.${kind}.${declarationID}`,
    payload,
  );
}

function installStoredPrintPayload(key: string, payload: unknown) {
  const values = new Map<string, string>();
  const storage: Storage = {
    get length() {
      return values.size;
    },
    clear() {
      values.clear();
    },
    getItem(key: string) {
      return values.get(key) ?? null;
    },
    key(index: number) {
      return Array.from(values.keys())[index] ?? null;
    },
    removeItem(key: string) {
      values.delete(key);
    },
    setItem(key: string, value: string) {
      values.set(key, value);
    },
  };

  Object.defineProperty(window, "localStorage", {
    configurable: true,
    value: storage,
  });
  storage.setItem(key, JSON.stringify(payload));
}

function dividendDocumentPayload() {
  return {
    declaration: {
      amount: { amount: 300000, currency: "GBP" },
      company_snapshot: {
        company_number: "137792C",
        director_name: "N. Meyer",
        legal_name: "NPM Limited",
        logo_asset_id: "logo-snapshot",
        logo_asset_url: "/api/identity/assets/logo-snapshot",
        logo_data_uri: dividendSnapshotLogoDataURI,
        registered_office: {
          country: "IM",
          line1: "18 Athol St",
          line2: "",
          locality: "Douglas",
          postal_code: "",
          region: "",
        },
        trading_name: "NPM Limited",
      },
      created_at: "2026-07-03T09:00:00Z",
      declared_date: "2026-07-03T00:00:00Z",
      headroom_snapshot: {
        as_of: "2026-07-03T00:00:00Z",
        available: { amount: 1716000, currency: "GBP" },
        distributable: true,
        financial_year: "2026-27",
        lines: [
          {
            amount: { amount: 1200000, currency: "GBP" },
            label: "Retained earnings b/fwd",
          },
          {
            amount: { amount: 516000, currency: "GBP" },
            label: "Profit YTD (after expenses)",
          },
          {
            amount: { amount: 0, currency: "GBP" },
            label: "Corporation tax provision at 0%",
          },
          {
            amount: { amount: 0, currency: "GBP" },
            label: "Dividends already declared YTD",
          },
          {
            amount: { amount: 1716000, currency: "GBP" },
            label: "Available to distribute",
          },
        ],
      },
      id: "dividend-2026-07",
      minutes_asset: null,
      per_share: { amount: 3000, currency: "GBP" },
      shareholder_name: "N. Meyer",
      shareholder_snapshot: {
        class: "ordinary £1",
        name: "N. Meyer",
        shares: 100,
      },
      shares: 100,
      voucher_asset: null,
      withholding_snapshot: {
        note: "No dividend withholding tax is deducted under the active jurisdiction pack (withholding: none).",
        policy: "none",
        tax_year: "2026-27",
      },
    },
  };
}

function dlaBalance() {
  return {
    balance: { amount_minor: 0, currency: "GBP" },
    policy: {
      bik_warning_key: "benefit_in_kind_interest_free",
      credit_explainer_template:
        "You can repay yourself up to {{ balance }} at any time with no tax consequence.",
      credit_status_text: "In credit — tax-free to withdraw",
      overdrawn_warning_template:
        "Your loan account is {{ balance }} overdrawn. The Isle of Man has no UK-style s455 charge, but an interest-free loan can create a taxable benefit in kind - charge interest at the official rate or clear it with a dividend.",
      remedy: "clear_with_dividend",
      s455_charge: false,
    },
    status: "credit",
    suggested_clearance: null,
  };
}

function unauthenticatedFetch() {
  return vi.fn(async () =>
    jsonResponse(
      {
        detail: "authentication required",
        status: 401,
        title: "Unauthorized",
        type: "about:blank",
      },
      401,
      "application/problem+json",
    ),
  );
}

function identityProfile() {
  return {
    bank_details: { bank_name: "", bic: "", iban: "" },
    company_number: "137792C",
    incorporation_date: "2020-07-14",
    legal_name: "NPM Limited",
    logo_asset_id: null,
    logo_asset_url: null,
    registered_office: {
      country: "IM",
      line1: "18 Athol St",
      line2: "",
      locality: "Douglas",
      postal_code: "",
      region: "",
    },
    shareholders: [
      {
        class: "ordinary £1",
        name: "N. Meyer",
        shares: 100,
      },
    ],
    directors: [{ appointed_date: "2020-07-14", is_chair: true, name: "N. Meyer" }],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

function dashboardSummary() {
  return {
    cash: {
      accounts: [],
      total_gbp: { amount: 0, currency: "GBP" },
    },
    dividendHeadroom: {
      available: { amount: 0, currency: "GBP" },
      distributable: true,
    },
    dla: {
      balance: { amount: 0, currency: "GBP" },
      status: "credit",
    },
    errors: [],
    greeting: {
      trading_name: "NPM Limited",
      user_name: "N. Meyer",
    },
    outstanding: {
      earliest_due_date: null,
      total_gbp: { amount: 0, currency: "GBP" },
      totals: [],
    },
    rate: {
      fetched_at: "2026-07-06T09:00:00Z",
      from: "EUR",
      rate: "0.8500",
      rate_date: "2026-07-06",
      source: "ECB daily",
      to: "GBP",
    },
    recentInvoices: [],
    toReconcile: {
      accounts: [],
      review_queue: [],
    },
  };
}

function pathFromRequest(input: RequestInfo | URL) {
  if (input instanceof Request) {
    return new URL(input.url, "http://localhost").pathname;
  }

  return new URL(String(input), "http://localhost").pathname;
}

function jsonResponse(
  body: unknown,
  status = 200,
  contentType = "application/json",
) {
  return new Response(JSON.stringify(body), {
    headers: {
      "Content-Type": contentType,
    },
    status,
  });
}
