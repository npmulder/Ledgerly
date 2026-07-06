import { expect, test, type Route } from "@playwright/test";

test("fixture invoices render, filter and search round-trip, and new invoice navigates", async ({
  page,
}) => {
  const state = invoicesState();
  await mockInvoicesApi(page, state);

  await page.goto("/invoices");

  await expect(
    page.getByRole("heading", { name: "Invoices" }),
  ).toBeVisible();
  const table = page.getByLabel("Invoices list");
  await expect(table.getByText("INV-2026-07")).toBeVisible();
  await expect(table.getByText("INV-2026-F2")).toBeVisible();
  await expect(table.getByText("OVERDUE 9D")).toBeVisible();
  await expect(table.getByText("€13,500.00 + £1,200.00")).toBeVisible();

  await page.getByRole("button", { name: /OVERDUE 1/ }).click();
  await expect(table.getByText("INV-2026-F2")).toBeVisible();
  await expect(table.getByText("INV-2026-07")).toHaveCount(0);

  await page.getByLabel("Search client or number").fill("contoso");
  await expect(
    page.getByRole("heading", { name: "All caught up" }),
  ).toBeVisible();

  await page.getByRole("button", { name: /ALL/ }).click();
  const searchedTable = page.getByLabel("Invoices list");
  await expect(searchedTable.getByText("INV-2026-07")).toBeVisible();
  await expect(searchedTable.getByText("INV-2026-F2")).toHaveCount(0);

  await page.getByRole("button", { name: /New invoice/ }).click();

  await expect(page).toHaveURL(/\/invoices\/invoice-new-draft$/);
  await expect(
    page.getByRole("heading", { name: "Invoice editor" }),
  ).toBeVisible();
  expect(state.createdClientId).toBe("client-contoso");
});

async function mockInvoicesApi(
  page: Parameters<typeof test>[0]["page"],
  state: InvoicesState,
) {
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
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
    if (path === "/api/invoicing/clients") {
      await fulfillJson(route, { clients: [contosoClient()] });
      return;
    }
    if (path === "/api/invoicing/invoices" && request.method() === "GET") {
      await fulfillJson(route, invoicesResponse(state.invoices, url));
      return;
    }
    if (path === "/api/invoicing/invoices" && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}");
      state.createdClientId = body.client_id;
      await fulfillJson(route, draftInvoice(body.client_id), 201);
      return;
    }

    await fulfillJson(
      route,
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
  });
}

type InvoiceCurrency = "EUR" | "GBP";
type InvoiceStatus = "draft" | "sent" | "paid" | "overdue";

type Money = {
  amount: number;
  currency: InvoiceCurrency;
};

type InvoiceTotals = {
  approx_gbp?: {
    amount: Money;
    as_of: string;
    locked: boolean;
    rate: {
      from: InvoiceCurrency;
      rate_date: string;
      source: string;
      to: InvoiceCurrency;
      value: string;
    };
  };
  subtotal: Money;
  total: Money;
  vat: Money;
};

type InvoiceFixture = {
  client_id: string;
  client_name: string;
  created_at: string;
  currency: InvoiceCurrency;
  days_overdue: number;
  due_date: string;
  id: string;
  issue_date: string;
  number: string | null;
  status: InvoiceStatus;
  totals: InvoiceTotals;
  updated_at: string;
};

type InvoicesState = {
  createdClientId: string | null;
  invoices: InvoiceFixture[];
};

function invoicesState(): InvoicesState {
  return {
    createdClientId: null,
    invoices: [
      invoiceListItem({
        client_name: "Contoso GmbH",
        id: "invoice-sent",
        number: "INV-2026-07",
        status: "sent",
        totals: euroTotals(450000, 384030, "0.8534"),
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
        totals: gbpTotals(120000),
      }),
      invoiceListItem({
        id: "invoice-paid-june",
        issue_date: "2026-06-01T00:00:00Z",
        number: "INV-2026-06",
        status: "paid",
        totals: euroTotals(450000, 381285, "0.8473"),
      }),
      invoiceListItem({
        id: "invoice-paid-may",
        issue_date: "2026-05-01T00:00:00Z",
        number: "INV-2026-05",
        status: "paid",
        totals: euroTotals(450000, 386100, "0.8580"),
      }),
    ],
  };
}

function invoicesResponse(invoices: InvoiceFixture[], url: URL) {
  const search = (url.searchParams.get("search") ?? "").toLowerCase();
  const statuses = url.searchParams.getAll("status");
  const searchMatches = invoices.filter(
    (invoice) =>
      !search ||
      invoice.client_name.toLowerCase().includes(search) ||
      (invoice.number ?? "").toLowerCase().includes(search),
  );
  const filtered = searchMatches.filter(
    (invoice) => statuses.length === 0 || statuses.includes(invoice.status),
  );

  return {
    counts: (["draft", "sent", "paid", "overdue"] as const).map((status) => ({
      count: searchMatches.filter((invoice) => invoice.status === status)
        .length,
      status,
    })),
    invoices: filtered,
    limit: Number(url.searchParams.get("limit") ?? 50),
    offset: Number(url.searchParams.get("offset") ?? 0),
    total_count: filtered.length,
    totals: totalsSummary(filtered),
  };
}

function totalsSummary(invoices: InvoiceFixture[]) {
  const subtotals = new Map<InvoiceCurrency, number>();
  let totalGBP = 0;

  for (const invoice of invoices) {
    const total = invoice.totals.total;
    subtotals.set(
      total.currency,
      (subtotals.get(total.currency) ?? 0) + total.amount,
    );
    totalGBP +=
      total.currency === "GBP"
        ? total.amount
        : (invoice.totals.approx_gbp?.amount.amount ?? 0);
  }

  return {
    subtotals: [...subtotals.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([currency, amount]) => ({ amount, currency })),
    total_gbp: { amount: totalGBP, currency: "GBP" },
  };
}

function invoiceListItem(
  overrides: Partial<InvoiceFixture>,
): InvoiceFixture {
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
    totals: euroTotals(0, 0, "0.8534"),
    updated_at: "2026-07-01T09:00:00Z",
    ...overrides,
  };
}

function euroTotals(
  amount: number,
  gbpAmount: number,
  rate: string,
): InvoiceTotals {
  return {
    approx_gbp: {
      amount: { amount: gbpAmount, currency: "GBP" },
      as_of: "2026-07-01T16:00:00Z",
      locked: true,
      rate: {
        from: "EUR",
        rate_date: "2026-07-01T16:00:00Z",
        source: "ECB",
        to: "GBP",
        value: rate,
      },
    },
    subtotal: { amount, currency: "EUR" },
    total: { amount, currency: "EUR" },
    vat: { amount: 0, currency: "EUR" },
  };
}

function gbpTotals(amount: number): InvoiceTotals {
  return {
    subtotal: { amount, currency: "GBP" },
    total: { amount, currency: "GBP" },
    vat: { amount: 0, currency: "GBP" },
  };
}

function draftInvoice(clientId: string) {
  return {
    client_id: clientId,
    created_at: "2026-07-06T12:00:00Z",
    currency: "EUR",
    due_date: "2026-07-20T00:00:00Z",
    id: "invoice-new-draft",
    issue_date: "2026-07-06T00:00:00Z",
    lines: [],
    lock_id: null,
    number: null,
    pdf_asset: null,
    settled_amount: null,
    settled_date: null,
    settlement_txn_ref: null,
    status: "draft",
    totals: euroTotals(0, 0, "0.8534"),
    updated_at: "2026-07-06T12:00:00Z",
    vat_treatment: "reverse-charge-eu-b2b",
  };
}

function contosoClient() {
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
    id: "client-contoso",
    name: "Contoso GmbH",
    retainer_amount: null,
    terms_days: 14,
    vat_number: "DE 129 273 398",
    vat_treatment: "reverse-charge-eu-b2b",
  };
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
    shareholders: [{ class: "ordinary £1", name: "N. Meyer", shares: 100 }],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

async function fulfillJson(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    body: JSON.stringify(body),
    contentType: "application/json",
    status,
  });
}
