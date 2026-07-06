import { expect, test, type Route } from "@playwright/test";

import type { DashboardSummary } from "@/api/dashboard";
import type {
  InvoicingClient,
  InvoicingInvoice,
  InvoicingInvoicePatch,
} from "@/api/invoicing";

test("renders seeded dashboard, raises retainer invoice, and follows list links", async ({
  page,
}) => {
  const state = dashboardState();
  await page.clock.setFixedTime(new Date("2026-07-06T09:00:00Z"));
  await mockDashboardApi(page, state);

  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  await expect(page.getByText("Good morning, N.")).toBeVisible();
  await expect(page.getByRole("main").getByText("NPM Limited")).toBeVisible();
  await expect(page.getByText("£23,720.68")).toBeVisible();
  await expect(page.getByText("£18,240.55 · €6,420.00")).toBeVisible();
  await expect(page.getByText("≈GBP £3,840.30 · due 15 Jul")).toBeVisible();
  await expect(page.getByText("£320.00 DR")).toBeVisible();
  await expect(page.getByText("£17,160.00")).toBeVisible();
  await expect(page.getByText("0.8534")).toBeVisible();
  await expect(page.getByText("STALE")).toBeVisible();
  await expect(page.getByText("INV-2026-07")).toBeVisible();
  await expect(page.getByText("CONTOSO GMBH SEPA")).toBeVisible();
  await expect(page.getByText("Retainer: Contoso GmbH")).toBeVisible();

  await page.screenshot({
    fullPage: true,
    path: "test-results/dashboard-screen-01.png",
  });

  await page.getByRole("button", { name: "Raise July invoice" }).click();

  await expect.poll(() => state.createRequests.length).toBe(1);
  await expect.poll(() => state.patchRequests.length).toBe(1);
  expect(state.createRequests[0]).toEqual({ client_id: "client_contoso" });
  expect(state.patchRequests[0]).toMatchObject({
    client_id: "client_contoso",
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
  });
  await expect(page).toHaveURL(/\/invoices\/inv_retainer$/);
  await expect(
    page.getByRole("heading", { name: "Invoice editor" }),
  ).toBeVisible();
  await expect(page.getByLabel("Description line 1")).toHaveValue(
    "July retainer",
  );

  await page.goto("/");
  await page.getByRole("link", { name: "INV-2026-07" }).click();
  await expect(page).toHaveURL(/\/invoices\/inv_2026_07$/);
  await expect(
    page.getByRole("heading", { name: "Invoice editor" }),
  ).toBeVisible();

  await page.goto("/");
  await page.getByRole("link", { name: "Open banking" }).click();
  await expect(page).toHaveURL(/\/banking$/);
  await expect(page.getByRole("heading", { name: "Banking" })).toBeVisible();
});

test("dashboard advisor refresh pulls a newly seeded insight", async ({
  page,
}) => {
  const state = dashboardState();
  await page.clock.setFixedTime(new Date("2026-07-06T09:00:00Z"));
  await mockDashboardApi(page, state);

  await page.goto("/");

  const panel = page.getByRole("region", { name: "Advisor panel" });
  await expect(panel).toContainText("No insights — all caught up");

  await panel.getByRole("button", { name: "Advisor actions" }).click();
  await page.getByRole("menuitem", { name: "Refresh insights" }).click();

  await expect(panel).toContainText("Freshly seeded dashboard insight");
  await page.screenshot({
    fullPage: true,
    path: "test-results/dashboard-advisor-refresh.png",
  });
});

async function mockDashboardApi(
  page: Parameters<typeof test>[0]["page"],
  state: DashboardState,
) {
  await page.route("**/*", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (!path.startsWith("/api/")) {
      await route.continue();
      return;
    }

    if (path === "/api/identity/me") {
      await fulfillJson(route, {
        created_at: "2026-07-05T12:00:00Z",
        email: "owner@example.com",
        id: 1,
        name: "N. Meyer",
      });
      return;
    }
    if (path === "/api/identity/profile") {
      await fulfillJson(route, identityProfile());
      return;
    }
    if (path === "/api/dashboard/summary") {
      await fulfillJson(route, dashboardSummaryFixture());
      return;
    }
    if (path === "/api/invoicing/clients") {
      await fulfillJson(route, { clients: clientsFixture() });
      return;
    }
    if (path === "/api/advisor/insights" && request.method() === "GET") {
      const surface = new URL(request.url()).searchParams.get("surface");
      await fulfillJson(route, {
        insights:
          surface === "dashboard" || surface === "banking"
            ? activeAdvisorInsights(state, surface)
            : [],
      });
      return;
    }
    if (
      path.startsWith("/api/advisor/insights/") &&
      path.endsWith("/dismiss") &&
      request.method() === "POST"
    ) {
      const key = decodeURIComponent(path.split("/").at(-2) ?? "");
      state.dismissedAdvisorKeys.add(key);
      await fulfillJson(route, undefined, 204);
      return;
    }
    if (path === "/api/advisor/refresh" && request.method() === "POST") {
      if (!state.refreshSeeded) {
        state.refreshSeeded = true;
        state.advisorInsights.push(dashboardAdvisorInsight());
      }
      await fulfillJson(route, advisorRefreshResponse());
      return;
    }
    if (path === "/api/invoicing/invoices" && request.method() === "POST") {
      state.createRequests.push(JSON.parse(request.postData() ?? "{}"));
      const draft = invoiceFixture({ id: "inv_retainer", lines: [] });
      state.invoices.set(draft.id, draft);
      await fulfillJson(route, draft, 201);
      return;
    }
    if (path.startsWith("/api/invoicing/invoices/")) {
      const invoiceID = decodeURIComponent(path.split("/").at(-1) ?? "");
      if (request.method() === "GET") {
        await fulfillJson(
          route,
          state.invoices.get(invoiceID) ?? invoiceFixture(),
        );
        return;
      }
      if (request.method() === "PATCH") {
        const patch = JSON.parse(
          request.postData() ?? "{}",
        ) as InvoicingInvoicePatch;
        state.patchRequests.push(patch);
        const invoice = patchedInvoice(invoiceID, patch);
        state.invoices.set(invoiceID, invoice);
        await fulfillJson(route, invoice);
        return;
      }
    }

    await fulfillJson(
      route,
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
  });
}

type DashboardState = {
  advisorInsights: DashboardAdvisorInsight[];
  createRequests: unknown[];
  dismissedAdvisorKeys: Set<string>;
  invoices: Map<string, InvoicingInvoice>;
  patchRequests: InvoicingInvoicePatch[];
  refreshSeeded: boolean;
};

function dashboardState(): DashboardState {
  const invoices = new Map<string, InvoicingInvoice>();
  invoices.set("inv_2026_07", invoiceFixture({ id: "inv_2026_07" }));
  return {
    advisorInsights: [],
    createRequests: [],
    dismissedAdvisorKeys: new Set(),
    invoices,
    patchRequests: [],
    refreshSeeded: false,
  };
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

function activeAdvisorInsights(state: DashboardState, surface: string | null) {
  return state.advisorInsights.filter(
    (insight) =>
      !state.dismissedAdvisorKeys.has(insight.key) &&
      (!surface || insight.surfaces.includes(surface)),
  );
}

function dashboardAdvisorInsight(): DashboardAdvisorInsight {
  return {
    bindings: {},
    created_at: "2026-07-06T09:00:00Z",
    cta: {
      action: "navigate:/reports",
      label: "Open reports",
    },
    key: "dashboard-refresh-insight",
    rendered_text: "Freshly seeded dashboard insight",
    rule_id: "filing_deadline_window",
    severity: "amber",
    surfaces: ["dashboard"],
  };
}

function advisorRefreshResponse() {
  return {
    run: {
      duration_ms: 1,
      finished_at: "2026-07-06T09:00:01Z",
      id: 1,
      insights_created: 1,
      insights_resolved: 0,
      insights_superseded: 0,
      started_at: "2026-07-06T09:00:00Z",
      trigger: "manual.RefreshNow",
      warnings: [],
    },
  };
}

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
      distributable: true,
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

function clientsFixture() {
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
      retainer_amount: { amount_minor: 450000, currency: "EUR" },
      terms_days: 14,
      vat_number: "DE129273398",
      vat_treatment: "reverse-charge-eu-b2b",
    },
  ] satisfies InvoicingClient[];
}

function patchedInvoice(
  invoiceID: string,
  patch: InvoicingInvoicePatch,
): InvoicingInvoice {
  return invoiceFixture({
    client_id: patch.client_id ?? "client_contoso",
    currency: patch.currency ?? "EUR",
    due_date: `${patch.due_date}T00:00:00Z`,
    id: invoiceID,
    issue_date: `${patch.issue_date}T00:00:00Z`,
    lines: (patch.lines ?? []).map((line, index) => ({
      description: line.description,
      id: line.id,
      invoice_id: invoiceID,
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
    id: "inv_2026_07",
    issue_date: "2026-07-06T00:00:00Z",
    lines: [
      {
        description: "July retainer",
        id: "line_retainer_2026_07",
        invoice_id: "inv_2026_07",
        line_total: { amount: 450000, currency: "EUR" },
        position: 1,
        qty: "1",
        unit_price: { amount: 450000, currency: "EUR" },
      },
    ],
    lock_id: null,
    number: null,
    pdf_asset: null,
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
    vat_treatment: "reverse-charge-eu-b2b",
    ...overrides,
  };
}

function identityProfile() {
  return {
    bank_details: {
      bank_name: "Revolut Business",
      bic: "REVOGB21",
      iban: "GB00REVO00000000000000",
    },
    company_number: "137792C",
    incorporation_date: "2024-07-14",
    legal_name: "NPM Limited",
    logo_asset_id: null,
    logo_asset_url: null,
    registered_office: {
      country: "Isle of Man",
      line1: "18 Athol Street",
      line2: "",
      locality: "Douglas",
      postal_code: "IM1 1JA",
      region: "",
    },
    shareholders: [],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

async function fulfillJson(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    body: status === 204 ? "" : JSON.stringify(body),
    contentType: "application/json",
    status,
  });
}
