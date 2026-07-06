import { cleanup, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type {
  InvoicingClient,
  InvoicingInvoiceListItem,
  InvoicingInvoicesResponse,
} from "@/api/invoicing";
import { InvoicesScreen } from "@/screens/InvoicesScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("InvoicesScreen", () => {
  it("renders pill counts, overdue tint and badge, and footer totals", async () => {
    vi.stubGlobal("fetch", invoicesFetch(invoicesResponse()));

    renderInvoicesScreen();

    expect(
      await screen.findByRole("heading", { level: 1, name: "Invoices" }),
    ).toBeInTheDocument();

    expect(
      await screen.findByRole("button", { name: /ALL 5/ }),
    ).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: /DRAFT 1/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /SENT 1/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /PAID 2/ })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /OVERDUE 1/ }),
    ).toBeInTheDocument();

    const table = await screen.findByLabelText("Invoices list");
    const overdueRow = within(table).getByText("INV-2026-F2").closest("tr");
    expect(overdueRow).toHaveClass("ui-table__row--overdue");
    expect(
      within(overdueRow as HTMLElement).getByText("OVERDUE 9D"),
    ).toBeInTheDocument();

    expect(
      within(table).getByText("Totals: €4,500.00 + £1,200.00 ≈ £5,040.30"),
    ).toBeInTheDocument();
  });

  it("renders the all-caught-up empty state when no invoices match", async () => {
    vi.stubGlobal("fetch", invoicesFetch(emptyInvoicesResponse()));

    renderInvoicesScreen();

    expect(
      await screen.findByRole("heading", { name: "All caught up" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("All caught up — no invoices match the current filters."),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("Invoices list")).not.toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: /ALL 0/ }),
    ).toBeInTheDocument();
  });
});

function renderInvoicesScreen() {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });

  render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <InvoicesScreen />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function invoicesFetch(response: InvoicingInvoicesResponse) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const path = pathFromRequest(input);
    if (path === "/api/invoicing/invoices") {
      return jsonResponse(response);
    }
    if (path === "/api/invoicing/clients") {
      return jsonResponse({ clients: [contosoClient()] });
    }
    return jsonResponse(
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
      "application/problem+json",
    );
  });
}

function invoicesResponse(): InvoicingInvoicesResponse {
  return {
    counts: [
      { count: 1, status: "draft" },
      { count: 1, status: "sent" },
      { count: 2, status: "paid" },
      { count: 1, status: "overdue" },
    ],
    invoices: [
      invoiceListItem({
        client_name: "Contoso GmbH",
        id: "invoice-sent",
        number: "INV-2026-07",
        status: "sent",
        totals: {
          approx_gbp: {
            amount: { amount: 384030, currency: "GBP" },
            as_of: "2026-07-01T16:00:00Z",
            locked: true,
            rate: {
              from: "EUR",
              rate_date: "2026-07-01T16:00:00Z",
              source: "ECB",
              to: "GBP",
              value: "0.8534",
            },
          },
          subtotal: { amount: 450000, currency: "EUR" },
          total: { amount: 450000, currency: "EUR" },
          vat: { amount: 0, currency: "EUR" },
        },
      }),
      invoiceListItem({
        client_id: "client-fabrikam",
        client_name: "Fabrikam Ltd",
        currency: "GBP",
        days_overdue: 9,
        due_date: "2026-06-19T00:00:00Z",
        id: "invoice-overdue",
        issue_date: "2026-06-10T00:00:00Z",
        number: "INV-2026-F2",
        status: "overdue",
        totals: {
          subtotal: { amount: 120000, currency: "GBP" },
          total: { amount: 120000, currency: "GBP" },
          vat: { amount: 0, currency: "GBP" },
        },
      }),
    ],
    limit: 50,
    offset: 0,
    total_count: 5,
    totals: {
      subtotals: [
        { amount: 450000, currency: "EUR" },
        { amount: 120000, currency: "GBP" },
      ],
      total_gbp: { amount: 504030, currency: "GBP" },
    },
  };
}

function emptyInvoicesResponse(): InvoicingInvoicesResponse {
  return {
    counts: [
      { count: 0, status: "draft" },
      { count: 0, status: "sent" },
      { count: 0, status: "paid" },
      { count: 0, status: "overdue" },
    ],
    invoices: [],
    limit: 50,
    offset: 0,
    total_count: 0,
    totals: {
      subtotals: [],
      total_gbp: { amount: 0, currency: "GBP" },
    },
  };
}

function invoiceListItem(
  overrides: Partial<InvoicingInvoiceListItem>,
): InvoicingInvoiceListItem {
  return {
    client_id: "client-contoso",
    client_name: "Contoso GmbH",
    created_at: "2026-07-01T09:00:00Z",
    currency: "EUR",
    days_overdue: 0,
    due_date: "2026-07-15T00:00:00Z",
    id: "invoice",
    issue_date: "2026-07-01T00:00:00Z",
    number: "INV-2026-07",
    status: "sent",
    totals: {
      subtotal: { amount: 0, currency: "EUR" },
      total: { amount: 0, currency: "EUR" },
      vat: { amount: 0, currency: "EUR" },
    },
    updated_at: "2026-07-01T09:00:00Z",
    ...overrides,
  };
}

function contosoClient(): InvoicingClient {
  return {
    address: {
      country: "DE",
      line1: "Theresienhöhe 12",
      line2: "",
      locality: "München",
      postal_code: "80339",
      region: "",
    },
    archived_at: null,
    created_at: "2026-07-01T09:00:00Z",
    day_rate: null,
    default_currency: "EUR",
    email: null,
    id: "client-contoso",
    name: "Contoso GmbH",
    retainer_amount: null,
    terms_days: 14,
    vat_number: "DE 129 273 398",
    vat_treatment: "reverse-charge-eu-b2b",
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
