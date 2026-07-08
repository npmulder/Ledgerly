import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { DashboardSummary } from "@/api/dashboard";
import type {
  InvoicingClient,
  InvoicingInvoice,
  InvoicingInvoicePatch,
} from "@/api/invoicing";
import { DashboardScreen } from "@/screens/DashboardScreen";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("DashboardScreen", () => {
  it("renders fixture stat cards, lists, and stale rate state", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-07-06T09:00:00Z"));
    vi.stubGlobal(
      "fetch",
      dashboardApi({
        clients: clientsFixture(),
        summary: dashboardSummaryFixture(),
      }).fetch,
    );

    renderDashboard();

    expect(await screen.findByText("Good morning, N.")).toBeInTheDocument();
    expect(screen.getByText("NPM Limited")).toBeInTheDocument();
    expect(screen.getByText("£23,720.68")).toBeInTheDocument();
    expect(screen.getByText("£18,240.55 · €6,420.00")).toBeInTheDocument();
    const outstandingCard = screen.getByText("Outstanding").closest("article");
    expect(outstandingCard).toHaveTextContent("€4,500.00");
    expect(outstandingCard).toHaveTextContent("≈GBP £3,840.30 · due 15 Jul");

    const dlaCard = screen.getByText("Director's loan").closest("article");
    expect(dlaCard).toHaveClass("dashboard-stat-card--amber");
    expect(dlaCard).toHaveTextContent("£320.00 DR");

    const headroomCard = screen
      .getByText("Dividend headroom")
      .closest("article");
    expect(headroomCard).toHaveClass("dashboard-stat-card--muted");
    expect(headroomCard).toHaveTextContent("£17,160.00");
    expect(headroomCard).toHaveTextContent("Not currently distributable");

    expect(screen.getByText("INV-2026-07")).toBeInTheDocument();
    expect(screen.getByText("OVERDUE 9D")).toBeInTheDocument();
    expect(screen.getAllByText("Contoso GmbH")).toHaveLength(5);
    expect(screen.getByText("Revolut EUR")).toBeInTheDocument();
    expect(screen.getByText("CONTOSO GMBH SEPA")).toBeInTheDocument();

    const rateCard = screen.getByText("0.8534").closest("section");
    expect(rateCard).toHaveClass("dashboard-rate-card--stale");
    expect(screen.getByText("STALE")).toBeInTheDocument();
    expect(
      screen.getByText("Frozen onto today's postings"),
    ).toBeInTheDocument();
  });

  it("creates a retainer draft via create and patch before navigating to the editor", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-07-06T09:00:00Z"));
    const user = userEvent.setup();
    const api = dashboardApi({
      clients: clientsFixture(),
      summary: dashboardSummaryFixture(),
    });
    vi.stubGlobal("fetch", api.fetch);

    renderDashboard();

    expect(
      await screen.findByText("Retainer: Contoso GmbH"),
    ).toBeInTheDocument();
    await user.click(
      await screen.findByRole("button", { name: "Raise July invoice" }),
    );

    expect(await screen.findByText("Editor landed")).toBeInTheDocument();
    expect(api.createRequests).toEqual([{ client_id: "client_contoso" }]);
    expect(api.patchRequests).toHaveLength(1);
    expect(api.patchRequests[0]).toMatchObject({
      client_id: "client_contoso",
      currency: "EUR",
      due_date: "2026-07-20",
      issue_date: "2026-07-06",
      lines: [
        {
          description: "July retainer",
          id: "line_retainer_2026_07",
          qty: "1",
          unit_price: { amount: 450000, currency: "EUR" },
        },
      ],
      vat_treatment: "reverse-charge-eu-b2b",
    });
  });

  it("computes retainer invoice dates at creation time", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-07-31T23:50:00Z"));
    const user = userEvent.setup();
    const api = dashboardApi({
      clients: clientsFixture(),
      summary: dashboardSummaryFixture(),
    });
    vi.stubGlobal("fetch", api.fetch);

    renderDashboard();

    const button = await screen.findByRole("button", {
      name: "Raise July invoice",
    });
    vi.setSystemTime(new Date("2026-08-01T09:00:00Z"));
    await user.click(button);

    expect(await screen.findByText("Editor landed")).toBeInTheDocument();
    expect(api.patchRequests[0]).toMatchObject({
      due_date: "2026-08-15",
      issue_date: "2026-08-01",
      lines: [
        {
          description: "August retainer",
          id: "line_retainer_2026_08",
        },
      ],
    });
  });

  it("falls back to the invoices screen when no retainer client exists", async () => {
    const user = userEvent.setup();
    const api = dashboardApi({
      clients: clientsFixture({ retainer: false }),
      summary: dashboardSummaryFixture(),
    });
    vi.stubGlobal("fetch", api.fetch);

    renderDashboard();

    await user.click(
      await screen.findByRole("button", { name: "New invoice" }),
    );

    expect(await screen.findByText("Invoices landed")).toBeInTheDocument();
    expect(api.createRequests).toEqual([]);
    expect(api.patchRequests).toEqual([]);
  });

  it("renders null sections as quiet unavailable states while healthy sections remain", async () => {
    vi.stubGlobal(
      "fetch",
      dashboardApi({
        clients: clientsFixture(),
        summary: {
          ...dashboardSummaryFixture(),
          cash: null,
          recentInvoices: null,
          errors: [{ detail: "cash failed", section: "cash" }],
        },
      }).fetch,
    );

    renderDashboard();

    expect(
      await screen.findByText("Some dashboard sections are unavailable."),
    ).toBeInTheDocument();
    const cashCard = screen.getByText("Cash").closest("article");
    expect(cashCard).toHaveTextContent("Unavailable");
    expect(screen.getByText("Recent invoices unavailable")).toBeInTheDocument();
    expect(screen.getAllByText("€4,500.00").length).toBeGreaterThan(0);
    expect(screen.getByText("Director's loan")).toBeInTheDocument();
  });

  it("keeps stat layout stable when a dashboard insight is dismissed", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      dashboardApi({
        advisorInsights: [
          {
            bindings: {},
            created_at: "2026-07-06T09:00:00Z",
            cta: {
              action: "navigate:/reports",
              label: "Open reports",
            },
            key: "dashboard-filing-warning",
            rendered_text: "VAT filing window opens soon.",
            rule_id: "filing_deadline_window",
            severity: "amber",
            surfaces: ["dashboard"],
          },
        ],
        clients: clientsFixture(),
        summary: dashboardSummaryFixture(),
      }).fetch,
    );

    renderDashboard();

    const panel = await screen.findByRole("region", {
      name: "Advisor panel",
    });
    await within(panel).findByText("VAT filing window opens soon.");
    expect(
      document.querySelectorAll(".dashboard-stat-grid article"),
    ).toHaveLength(4);
    expect(screen.getByText("£23,720.68")).toBeInTheDocument();
    expect(screen.getByText("£320.00 DR")).toBeInTheDocument();

    await user.click(
      within(panel).getByRole("button", {
        name: /Dismiss advisor insight: VAT filing window opens soon/,
      }),
    );

    await waitFor(() => {
      expect(
        screen.queryByText("VAT filing window opens soon."),
      ).not.toBeInTheDocument();
    });
    expect(
      await within(panel).findByText("No insights — all caught up"),
    ).toBeVisible();
    expect(
      document.querySelectorAll(".dashboard-stat-grid article"),
    ).toHaveLength(4);
    expect(screen.getByText("£23,720.68")).toBeInTheDocument();
    expect(screen.getByText("£320.00 DR")).toBeInTheDocument();
  });
});

function renderDashboard() {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <QueryClientProvider client={queryClient}>
        <Routes>
          <Route path="/" element={<DashboardScreen />} />
          <Route path="/invoices" element={<p>Invoices landed</p>} />
          <Route path="/invoices/:id" element={<p>Editor landed</p>} />
          <Route path="/banking" element={<p>Banking landed</p>} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function dashboardApi({
  advisorInsights = [],
  clients,
  summary,
}: {
  advisorInsights?: DashboardAdvisorInsight[];
  clients: InvoicingClient[];
  summary: DashboardSummary;
}) {
  const createRequests: unknown[] = [];
  const dismissedAdvisorKeys = new Set<string>();
  const patchRequests: InvoicingInvoicePatch[] = [];
  const api = {
    createRequests,
    patchRequests,
    fetch: vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathFromRequest(input);
      const method = init?.method ?? "GET";

      if (path === "/api/dashboard/summary") {
        return jsonResponse(summary);
      }
      if (path === "/api/invoicing/clients") {
        return jsonResponse({ clients });
      }
      if (path === "/api/advisor/insights" && method === "GET") {
        return jsonResponse({
          insights: advisorInsights.filter(
            (insight) => !dismissedAdvisorKeys.has(insight.key),
          ),
        });
      }
      if (
        path.startsWith("/api/advisor/insights/") &&
        path.endsWith("/dismiss") &&
        method === "POST"
      ) {
        dismissedAdvisorKeys.add(
          decodeURIComponent(path.split("/").at(-2) ?? ""),
        );
        return jsonResponse(undefined, 204);
      }
      if (path === "/api/advisor/refresh" && method === "POST") {
        return jsonResponse({
          run: {
            duration_ms: 1,
            finished_at: "2026-07-06T09:00:01Z",
            id: 1,
            insights_created: 0,
            insights_resolved: 0,
            insights_superseded: 0,
            started_at: "2026-07-06T09:00:00Z",
            trigger: "manual.RefreshNow",
            warnings: [],
          },
        });
      }
      if (path === "/api/invoicing/invoices" && method === "POST") {
        createRequests.push(JSON.parse(String(init?.body)));
        return jsonResponse(draftInvoice(), 201);
      }
      if (
        path === "/api/invoicing/invoices/inv_retainer" &&
        method === "PATCH"
      ) {
        const patch = JSON.parse(String(init?.body)) as InvoicingInvoicePatch;
        patchRequests.push(patch);
        return jsonResponse(patchedInvoice(patch));
      }
      return jsonResponse(
        { status: 404, title: "Not Found", type: "about:blank" },
        404,
        "application/problem+json",
      );
    }),
  };
  return api;
}

type DashboardAdvisorInsight = {
  bindings: Record<string, unknown>;
  created_at: string;
  cta: { action: string; label: string; params?: Record<string, unknown> };
  key: string;
  rendered_text: string;
  rule_id: string;
  severity: "amber" | "teal";
  surfaces: string[];
};

function dashboardSummaryFixture(): DashboardSummary {
  return {
    cash: {
      accounts: [
        {
          currency: "GBP",
          gbp_balance: { amount: 1824055, currency: "GBP" },
          id: 1,
          ledger_account_code: "1000-cash-gbp",
          name: "Revolut GBP",
          native_balance: { amount: 1824055, currency: "GBP" },
          provider: "revolut",
        },
        {
          currency: "EUR",
          gbp_balance: { amount: 548013, currency: "GBP" },
          id: 2,
          ledger_account_code: "1001-cash-eur",
          name: "Revolut EUR",
          native_balance: { amount: 642000, currency: "EUR" },
          provider: "revolut",
        },
      ],
      total_gbp: { amount: 2372068, currency: "GBP" },
    },
    dividendHeadroom: {
      available: { amount: 1716000, currency: "GBP" },
      distributable: false,
    },
    dla: {
      balance: { amount: -32000, currency: "GBP" },
      status: "overdrawn",
    },
    errors: [],
    greeting: {
      trading_name: "NPM Limited",
      user_name: "N. Meyer",
    },
    outstanding: {
      earliest_due_date: "2026-07-15",
      total_gbp: { amount: 384030, currency: "GBP" },
      totals: [{ amount: 450000, currency: "EUR" }],
    },
    rate: {
      fetched_at: "2026-07-06T08:30:00Z",
      from: "EUR",
      rate: "0.8534",
      rate_date: "2026-07-05",
      source: "ECB daily",
      to: "GBP",
    },
    recentInvoices: [
      recentInvoice("inv_2026_07", "INV-2026-07", "sent", null),
      recentInvoice("inv_2026_06", "INV-2026-06", "paid", null),
      recentInvoice("inv_2026_05", "INV-2026-05", "paid", null),
      recentInvoice("inv_2026_04", "INV-2026-04", "overdue", 9),
      recentInvoice("inv_2026_03", "INV-2026-03", "draft", null),
    ],
    toReconcile: {
      accounts: [
        {
          currency: "GBP",
          id: 1,
          ledger_account_code: "1000-cash-gbp",
          name: "Revolut GBP",
          unreconciled_count: 1,
        },
        {
          currency: "EUR",
          id: 2,
          ledger_account_code: "1001-cash-eur",
          name: "Revolut EUR",
          unreconciled_count: 2,
        },
      ],
      review_queue: [
        {
          amount: { amount: 450000, currency: "EUR" },
          confidence: 0.98,
          kind: "invoice-match",
          payee: "CONTOSO GMBH SEPA",
        },
        {
          amount: { amount: -240000, currency: "GBP" },
          confidence: 0.88,
          kind: "dla",
          payee: "TRANSFER TO N MEYER",
        },
        {
          amount: { amount: -890, currency: "EUR" },
          confidence: 0.72,
          kind: "payee-rule",
          payee: "HETZNER ONLINE",
        },
      ],
    },
  };
}

function recentInvoice(
  id: string,
  number: string,
  status: string,
  daysOverdue: number | null,
) {
  return {
    amount: { amount: 450000, currency: "EUR" },
    client: "Contoso GmbH",
    days_overdue: daysOverdue,
    id,
    number,
    status,
  };
}

function clientsFixture({ retainer = true }: { retainer?: boolean } = {}) {
  return [
    {
      address: {
        country: "DE",
        line1: "1 Main St",
        line2: "",
        locality: "Munich",
        postal_code: "80331",
        region: "",
      },
      archived_at: null,
      created_at: "2026-07-01T00:00:00Z",
      day_rate: null,
      default_currency: "EUR",
      email: "billing@contoso.example",
      id: "client_contoso",
      name: "Contoso GmbH",
      retainer_amount: retainer
        ? { amount_minor: 450000, currency: "EUR" }
        : null,
      terms_days: 14,
      vat_number: "DE129273398",
      vat_treatment: "reverse-charge-eu-b2b",
    },
  ] satisfies InvoicingClient[];
}

function draftInvoice(): InvoicingInvoice {
  return invoiceFixture({ id: "inv_retainer", lines: [] });
}

function patchedInvoice(patch: InvoicingInvoicePatch): InvoicingInvoice {
  return invoiceFixture({
    client_id: patch.client_id ?? "client_contoso",
    currency: patch.currency ?? "EUR",
    due_date: `${patch.due_date}T00:00:00Z`,
    id: "inv_retainer",
    issue_date: `${patch.issue_date}T00:00:00Z`,
    lines: (patch.lines ?? []).map((line, index) => ({
      description: line.description,
      id: line.id,
      invoice_id: "inv_retainer",
      line_total: line.unit_price,
      position: index + 1,
      qty: line.qty,
      unit_price: line.unit_price,
    })),
    vat_treatment: patch.vat_treatment ?? "reverse-charge-eu-b2b",
  });
}

function invoiceFixture(
  overrides: Partial<InvoicingInvoice> = {},
): InvoicingInvoice {
  return {
    client_id: "client_contoso",
    created_at: "2026-07-06T09:00:00Z",
    currency: "EUR",
    due_date: "2026-07-20T00:00:00Z",
    id: "inv_retainer",
    issue_date: "2026-07-06T00:00:00Z",
    lines: [],
    lock_id: null,
    number: null,
    pdf_asset: null,
    recurring_run_date: null,
    recurring_template_id: null,
    sent_at: null,
    settled_amount: null,
    settled_date: null,
    settlement_txn_ref: null,
    status: "draft",
    totals: {
      approx_gbp: null,
      subtotal: { amount: 450000, currency: "EUR" },
      total: { amount: 450000, currency: "EUR" },
      vat: { amount: 0, currency: "EUR" },
    },
    updated_at: "2026-07-06T09:00:00Z",
    vat_registered: true,
    vat_treatment: "reverse-charge-eu-b2b",
    ...overrides,
  };
}

function pathFromRequest(input: RequestInfo | URL) {
  if (input instanceof Request) {
    return new URL(input.url).pathname;
  }
  return new URL(String(input), window.location.origin).pathname;
}

function jsonResponse(
  body: unknown,
  status = 200,
  contentType = "application/json",
) {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": contentType },
    status,
  });
}
