import { expect, test, type Route } from "@playwright/test";

import type {
  InvoicingClient,
  InvoicingInvoice,
  InvoicingInvoicePatch,
} from "@/api/invoicing";

test("creates, edits, autosaves, sends, shows locked rate, and reverts same-day", async ({
  page,
}) => {
  const state = invoiceState();
  await mockInvoiceApi(page, state);

  await page.goto("/invoices");

  await expect(
    page.getByRole("heading", { level: 1, name: "Invoices" }),
  ).toBeVisible();
  await page.getByRole("button", { name: /New invoice/ }).click();

  await expect(page).toHaveURL(/\/invoices\/inv_1$/);
  await expect(
    page.getByRole("heading", { name: "Invoice editor" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Add line" }).click();
  await page.getByLabel("Description line 1").fill("July platform work");
  await page.getByLabel("Quantity line 1").fill("2");
  await page.getByLabel("Unit price line 1").fill("650.00");

  await expect.poll(() => state.patchRequests.length).toBeGreaterThan(0);
  await expect(page.getByRole("status")).toContainText("Saved");
  await expect(page.getByText("€1,560.00")).toBeVisible();

  await page
    .getByLabel("VAT treatment")
    .selectOption("reverse-charge-eu-b2b");

  await expect.poll(() => state.patchRequests.at(-1)?.vat_treatment).toBe(
    "reverse-charge-eu-b2b",
  );
  await expect(page.getByText("€0.00")).toBeVisible();
  await expect(page.getByText("€1,300.00").first()).toBeVisible();

  await page.getByRole("button", { name: "Send invoice" }).click();

  await expect(page.getByText("INV-2026-1").first()).toBeVisible();
  await expect(page.getByText("Source: ECB 2026-07-06")).toBeVisible();
  await expect(page.getByText("🔒 0.850000000000000000")).toBeVisible();
  await page.screenshot({
    fullPage: true,
    path: "test-results/invoice-editor-sent.png",
  });

  await page.getByRole("button", { name: "Revert same-day" }).click();

  await expect(page.getByRole("button", { name: "Send invoice" })).toBeVisible();
  await expect(page.getByText("≈ 0.85")).toBeVisible();
});

test("sends overdue reminder from list advisor and logs it in editor", async ({
  page,
}) => {
  const state = invoiceState();
  state.invoice = overdueInvoice();
  await mockInvoiceApi(page, state);

  await page.goto("/invoices");

  const advisor = page.getByRole("region", { name: "Invoice advisor" });
  await expect(advisor).toContainText("9 days overdue");
  await advisor.getByRole("button", { name: "Send reminder" }).click();

  await expect(page.getByRole("status")).toContainText(
    "Reminder sent for INV-2026-1.",
  );
  await expect.poll(() => state.reminderRequests.length).toBe(1);

  await page.getByRole("link", { name: "INV-2026-1" }).click();

  await expect(
    page.getByRole("heading", { name: "Invoice editor" }),
  ).toBeVisible();
  await expect(page.getByText("Reminder sent").first()).toBeVisible();
  await expect(page.getByText(/6 Jul 2026/)).toBeVisible();
});

async function mockInvoiceApi(
  page: Parameters<typeof test>[0]["page"],
  state: InvoiceState,
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
    if (path === "/api/invoicing/clients") {
      await fulfillJson(route, { clients: clientsFixture() });
      return;
    }
    if (path === "/api/invoicing/invoices" && request.method() === "GET") {
      await fulfillJson(route, invoicesResponse(state.invoice));
      return;
    }
    if (path === "/api/invoicing/invoices" && request.method() === "POST") {
      state.invoice = draftInvoice();
      await fulfillJson(route, state.invoice, 201);
      return;
    }
    if (path === "/api/invoicing/invoices/inv_1" && request.method() === "GET") {
      await fulfillJson(route, state.invoice ?? draftInvoice());
      return;
    }
    if (
      path === "/api/invoicing/invoices/inv_1" &&
      request.method() === "PATCH"
    ) {
      const patch = JSON.parse(
        request.postData() ?? "{}",
      ) as InvoicingInvoicePatch;
      state.patchRequests.push(patch);
      state.invoice = applyPatch(state.invoice ?? draftInvoice(), patch);
      await fulfillJson(route, state.invoice);
      return;
    }
    if (
      path === "/api/invoicing/invoices/inv_1/send" &&
      request.method() === "POST"
    ) {
      state.invoice = sentInvoice(state.invoice ?? draftInvoice());
      await fulfillJson(route, {
        invoice: state.invoice,
        locked_rate: {
          from: "EUR",
          id: 7,
          rate: "0.850000000000000000",
          rate_date: "2026-07-06",
          source: "ECB",
          to: "GBP",
        },
        number: "INV-2026-1",
      });
      return;
    }
    if (
      path === "/api/invoicing/invoices/inv_1/remind" &&
      request.method() === "POST"
    ) {
      const reminder = {
        invoice_id: "inv_1",
        sent_at: "2026-07-06T13:15:00Z",
      };
      state.reminderRequests.push("inv_1");
      state.invoice = {
        ...(state.invoice ?? overdueInvoice()),
        reminders: [reminder, ...(state.invoice?.reminders ?? [])],
      };
      await fulfillJson(route, {
        invoice: state.invoice,
        reminder,
      });
      return;
    }
    if (
      path === "/api/invoicing/invoices/inv_1/revert" &&
      request.method() === "POST"
    ) {
      state.invoice = {
        ...(state.invoice ?? draftInvoice()),
        lock_id: null,
        number: null,
        reminders: [],
        sent_at: null,
        status: "draft",
        totals: withApprox((state.invoice ?? draftInvoice()).totals),
      };
      await fulfillJson(route, state.invoice);
      return;
    }

    await fulfillJson(
      route,
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
  });
}

type InvoiceState = {
  invoice: InvoicingInvoice | null;
  patchRequests: InvoicingInvoicePatch[];
  reminderRequests: string[];
};

function invoiceState(): InvoiceState {
  return { invoice: null, patchRequests: [], reminderRequests: [] };
}

function clientsFixture(): InvoicingClient[] {
  return [
    {
      address: {
        country: "DE",
        line1: "1 Main St",
        line2: "",
        locality: "Berlin",
        postal_code: "10115",
        region: "",
      },
      archived_at: null,
      created_at: "2026-07-01T00:00:00Z",
      day_rate: null,
      default_currency: "EUR",
      email: "billing@contoso.example",
      id: "client_contoso",
      name: "Contoso GmbH",
      retainer_amount: null,
      terms_days: 14,
      vat_number: "DE123",
      vat_treatment: "reverse-charge-eu-b2b",
    },
  ];
}

function draftInvoice(
  overrides: Partial<InvoicingInvoice> = {},
): InvoicingInvoice {
  const invoice = {
    client_id: "client_contoso",
    created_at: "2026-07-06T10:00:00Z",
    currency: "EUR",
    due_date: "2026-07-20T00:00:00Z",
    id: "inv_1",
    issue_date: "2026-07-06T00:00:00Z",
    lines: [],
    lock_id: null,
    number: null,
    pdf_asset: null,
    reminders: [],
    sent_at: null,
    settled_amount: null,
    settled_date: null,
    settlement_txn_ref: null,
    status: "draft",
    totals: totals("EUR", 0, 0),
    updated_at: "2026-07-06T10:00:00Z",
    vat_treatment: "domestic",
    ...overrides,
  };
  return invoice;
}

function sentInvoice(invoice: InvoicingInvoice): InvoicingInvoice {
  return {
    ...invoice,
    lock_id: "7",
    number: "INV-2026-1",
    pdf_asset: "/api/identity/assets/invoice-pdf",
    sent_at: "2026-07-06T12:00:00Z",
    status: invoice.status === "overdue" ? "overdue" : "sent",
    totals: {
      ...invoice.totals,
      approx_gbp: null,
    },
    updated_at: "2026-07-06T12:00:00Z",
  };
}

function overdueInvoice(): InvoicingInvoice {
  return sentInvoice(
    draftInvoice({
      due_date: "2026-06-27T00:00:00Z",
      issue_date: "2026-06-01T00:00:00Z",
      lines: [
        {
          description: "June support",
          id: "line_overdue",
          invoice_id: "inv_1",
          line_total: { amount: 120_000, currency: "EUR" },
          position: 1,
          qty: "1",
          unit_price: { amount: 120_000, currency: "EUR" },
        },
      ],
      status: "overdue",
      totals: {
        approx_gbp: null,
        subtotal: { amount: 120_000, currency: "EUR" },
        total: { amount: 120_000, currency: "EUR" },
        vat: { amount: 0, currency: "EUR" },
      },
      vat_treatment: "reverse-charge-eu-b2b",
    }),
  );
}

function applyPatch(
  invoice: InvoicingInvoice,
  patch: InvoicingInvoicePatch,
): InvoicingInvoice {
  const currency = patch.currency ?? invoice.currency;
  const vatTreatment = patch.vat_treatment ?? invoice.vat_treatment;
  const lines = (patch.lines ?? invoice.lines).map(
    (line, index) => {
      const amount = Math.round(Number(line.qty) * line.unit_price.amount);
      return {
        description: line.description,
        id: line.id,
        invoice_id: invoice.id,
        line_total: { amount, currency },
        position: index + 1,
        qty: line.qty,
        unit_price: { amount: line.unit_price.amount, currency },
      };
    },
  );
  const subtotal = lines.reduce(
    (sum, line) => sum + line.line_total.amount,
    0,
  );
  const vat = vatTreatment === "domestic" ? Math.round(subtotal * 0.2) : 0;
  return {
    ...invoice,
    client_id: patch.client_id ?? invoice.client_id,
    currency,
    due_date: patch.due_date
      ? `${patch.due_date}T00:00:00Z`
      : invoice.due_date,
    issue_date: patch.issue_date
      ? `${patch.issue_date}T00:00:00Z`
      : invoice.issue_date,
    lines,
    totals: totals(currency, subtotal, vat),
    updated_at: new Date().toISOString(),
    vat_treatment: vatTreatment,
  };
}

function totals(
  currency: InvoicingInvoice["currency"],
  subtotal: number,
  vat: number,
): InvoicingInvoice["totals"] {
  return withApprox({
    subtotal: { amount: subtotal, currency },
    total: { amount: subtotal + vat, currency },
    vat: { amount: vat, currency },
  });
}

function withApprox(
  baseTotals: InvoicingInvoice["totals"],
): InvoicingInvoice["totals"] {
  return {
    ...baseTotals,
    approx_gbp: {
      amount: {
        amount: Math.round(baseTotals.total.amount * 0.85),
        currency: "GBP",
      },
      as_of: "2026-07-06T09:00:00Z",
      locked: false,
      rate: {
        from: baseTotals.total.currency,
        rate_date: "2026-07-05T00:00:00Z",
        source: "ECB",
        to: "GBP",
        value: "0.85",
      },
    },
  };
}

function invoicesResponse(invoice: InvoicingInvoice | null) {
  return {
    counts: [],
    invoices: invoice
      ? [
          {
            client_id: invoice.client_id,
            client_name: "Contoso GmbH",
            created_at: invoice.created_at,
            currency: invoice.currency,
            days_overdue: invoice.status === "overdue" ? 9 : 0,
            due_date: invoice.due_date,
            id: invoice.id,
            issue_date: invoice.issue_date,
            number: invoice.number,
            status: invoice.status,
            totals: invoice.totals,
            updated_at: invoice.updated_at,
          },
        ]
      : [],
    limit: 20,
    offset: 0,
    total_count: invoice ? 1 : 0,
    totals: {
      subtotals: [],
      total_gbp: { amount: 0, currency: "GBP" },
    },
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
