import { expect, test, type Page, type Route } from "@playwright/test";

const expected = {
  cashAfterPayment: "£3,885.30",
  dividendHeadroom: "£3,000.00",
  dividendPersonalTax: "set aside personally £300.00",
  dividendPrefill: "3000.00",
  fxGain: "£45.00",
  invoiceGBP: "£3,840.30",
  invoiceNative: "€4,500.00",
  lockedRate: "0.8534",
  netProfit: "£3,885.30",
  outstandingAfterInvoice: "≈GBP £3,840.30 · due 20 Jul",
  outstandingAfterPayment: "≈GBP £0.00 · no due date",
  turnover: "£3,885.30",
};

test("v1 app walkthrough @walkthrough", async ({ page }) => {
  const state = walkthroughState();
  await page.clock.setFixedTime(state.now);
  await mockWalkthroughApi(page, state);

  await page.goto("/");
  await expect(page).toHaveURL(/\/login$/);
  await page.getByLabel("Email").fill("owner@example.com");
  await page.getByLabel("Password").fill("correct horse battery staple");
  await page.getByRole("button", { name: "Login" }).click();

  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  await expect(page.getByText("Good morning, N.")).toBeVisible();
  await expect(page.getByText("No recent invoices")).toBeVisible();
  await expect(page.getByText("No review queue items.")).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Advisor panel" }),
  ).toContainText("No insights — all caught up");
  await expect(page.getByText("Cash (GBP equiv.)").locator("..")).toContainText(
    "£0.00",
  );
  await expect(page.getByText("Outstanding").locator("..")).toContainText(
    "£0.00",
  );

  await page.goto("/settings/clients");
  await expect(
    page.getByRole("heading", { exact: true, name: "Clients" }),
  ).toBeVisible();
  await page.getByLabel("Client name").fill("Contoso GmbH");
  await page.getByLabel("Billing email").fill("billing@contoso.example");
  await page.getByLabel("Currency").selectOption("EUR");
  await page.getByLabel("Terms").selectOption("14");
  await page.getByLabel("VAT treatment").selectOption("reverse-charge-eu-b2b");
  await page.getByLabel("VAT number").fill("DE129273398");
  await page.getByLabel("Monthly retainer").fill("4500.00");
  await page.getByLabel("Client address line 1").fill("Theresienhoehe 12");
  await page.getByLabel("Client address locality").fill("Munich");
  await page.getByLabel("Client address country").fill("DE");
  await page.getByRole("button", { name: "Save client" }).click();
  await expect(page.getByText("Retainer €4,500.00 / month")).toBeVisible();

  await page.goto("/");
  await expect(page.getByText("Retainer: Contoso GmbH")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Raise July invoice" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Raise July invoice" }).click();

  await expect(page).toHaveURL(/\/invoices\/inv-july-retainer$/);
  await expect(
    page.getByRole("heading", { name: "Invoice editor" }),
  ).toBeVisible();
  await expect(page.getByLabel("Description line 1")).toHaveValue(
    "July retainer",
  );
  await expect(page.getByLabel("Locked FX rate")).toContainText(
    expected.lockedRate,
  );
  await expect(
    page.getByText(`${expected.invoiceGBP} indicative until send`),
  ).toBeVisible();

  await page.getByRole("button", { name: "Send invoice" }).click();
  await expect(page.getByText("INV-2026-0001").first()).toBeVisible();
  await expect(page.getByLabel("Locked FX rate")).toContainText("Source: ECB");
  await expect(page.getByLabel("Locked FX rate")).toContainText(
    expected.lockedRate,
  );
  await expect(page.getByText(`${expected.invoiceGBP} locked`)).toBeVisible();

  await page.goto("/");
  await expect(page.getByText("Cash (GBP equiv.)").locator("..")).toContainText(
    "£0.00",
  );
  await expect(
    page.getByText("Outstanding", { exact: true }).locator(".."),
  ).toContainText(expected.invoiceNative);
  await expect(
    page.getByText("Outstanding", { exact: true }).locator(".."),
  ).toContainText(expected.outstandingAfterInvoice);

  await page.goto("/banking");
  await expect(page.getByRole("heading", { name: "Banking" })).toBeVisible();
  const chooserPromise = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Import CSV" }).click();
  const chooser = await chooserPromise;
  await chooser.setFiles({
    buffer: Buffer.from(
      "Date,Description,Amount,Currency\n2026-07-06,CONTOSO GMBH SEPA INV-2026-0001,4500.00,EUR\n",
    ),
    mimeType: "text/csv",
    name: "contoso-payment.csv",
  });
  await expect(
    page.getByText("contoso-payment.csv: 1 new, 0 duplicates"),
  ).toBeVisible();
  await expect(page.getByText("Invoice match")).toBeVisible();
  await expect(page.getByText("CONTOSO GMBH SEPA")).toBeVisible();
  await expect(page.getByText(expected.invoiceNative)).toBeVisible();

  await page.getByRole("button", { name: "Confirm" }).click();
  await expect(
    page.getByText(`Confirmed match - auto-posted FX gain ${expected.fxGain}`),
  ).toBeVisible();
  await expect(page.getByText("Invoice match")).toBeHidden();

  await page.goto("/");
  await expect(page.getByText("Cash (GBP equiv.)").locator("..")).toContainText(
    expected.cashAfterPayment,
  );
  await expect(
    page.getByText("Outstanding", { exact: true }).locator(".."),
  ).toContainText("£0.00");
  await expect(
    page.getByText("Outstanding", { exact: true }).locator(".."),
  ).toContainText(expected.outstandingAfterPayment);
  await expect(page.getByText("INV-2026-0001")).toBeVisible();
  await expect(page.getByText("PAID")).toBeVisible();

  state.now = new Date("2026-07-24T09:00:00Z");
  await page.clock.setFixedTime(state.now);
  await page.reload();
  const advisorPanel = page.getByRole("region", { name: "Advisor panel" });
  await expect(advisorPanel).toContainText("VAT return is due 30 Jul");

  await expect(page.getByText("Dividend headroom").locator("..")).toContainText(
    expected.dividendHeadroom,
  );
  await page.getByRole("link", { name: "Declare dividend" }).click();
  await expect(page).toHaveURL(/\/dividends\?amount=3000\.00$/);
  await expect(page.getByLabel("Amount")).toHaveValue(expected.dividendPrefill);
  await expect(page.getByText("Within headroom")).toBeVisible();
  await expect(page.getByText(expected.dividendPersonalTax)).toBeVisible();
  await page
    .getByRole("button", { name: "Generate voucher + minutes" })
    .click();
  await expect(page.getByText("Dividend declared")).toBeVisible();
  await expect(page.getByLabel("Dividend history")).toContainText(
    expected.dividendHeadroom,
  );

  await page.goto("/dla");
  await expect(
    page.getByRole("heading", { name: "Director's loan · N. Meyer" }),
  ).toBeVisible();
  await expect(page.getByRole("status").getByText("£3,000.00 CR"))
    .toBeVisible();
  await expect(page.getByLabel("DLA running ledger")).toContainText(
    "Dividend declared",
  );

  await page.goto("/reports");
  await expect(page.getByRole("heading", { name: "Reports" })).toBeVisible();
  await page.getByRole("button", { name: "Jul-Sep" }).click();
  const pl = page.getByLabel("P&L lines");
  await expect(pl).toContainText("Consulting income - Contoso GmbH (EUR)");
  await expect(pl).toContainText(expected.invoiceGBP);
  await expect(pl).toContainText("Realised FX gains on settlement");
  await expect(pl).toContainText(expected.fxGain);
  await expect(pl).toContainText(expected.turnover);
  await expect(pl).toContainText(expected.netProfit);

  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export pack" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe(
    "ledgerly-export-2026-07-01_2026-09-30.zip",
  );
  await expect(page.getByText("Export pack is being prepared.")).toBeVisible();
});

async function mockWalkthroughApi(page: Page, state: WalkthroughState) {
  await page.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;

    if (!path.startsWith("/api/")) {
      await route.continue();
      return;
    }

    if (path === "/api/identity/me") {
      if (!state.authenticated) {
        await fulfillJson(
          route,
          { status: 401, title: "Unauthorized", type: "about:blank" },
          401,
        );
        return;
      }
      await fulfillJson(route, currentUser());
      return;
    }
    if (path === "/api/identity/login" && request.method() === "POST") {
      state.authenticated = true;
      await fulfillJson(route, currentUser());
      return;
    }
    if (path === "/api/identity/profile") {
      await fulfillJson(route, identityProfile());
      return;
    }
    if (path === "/api/dashboard/summary") {
      await fulfillJson(route, dashboardSummary(state));
      return;
    }
    if (path === "/api/invoicing/clients" && request.method() === "GET") {
      await fulfillJson(route, { clients: state.clients });
      return;
    }
    if (path === "/api/invoicing/clients" && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}") as ClientRequest;
      const client = clientFromRequest(body);
      state.clients = [client, ...state.clients];
      await fulfillJson(route, client, 201);
      return;
    }
    if (path === "/api/invoicing/invoices" && request.method() === "GET") {
      await fulfillJson(route, invoicesResponse(state));
      return;
    }
    if (path === "/api/invoicing/invoices" && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}") as {
        client_id: string;
      };
      const invoice = draftInvoice(body.client_id);
      state.invoices.set(invoice.id, invoice);
      await fulfillJson(route, invoice, 201);
      return;
    }
    const invoiceMatch = path.match(/^\/api\/invoicing\/invoices\/([^/]+)$/);
    if (invoiceMatch) {
      const invoiceID = decodeURIComponent(invoiceMatch[1]);
      if (request.method() === "GET") {
        await fulfillJson(route, state.invoices.get(invoiceID) ?? notFound());
        return;
      }
      if (request.method() === "PATCH") {
        const patch = JSON.parse(request.postData() ?? "{}") as InvoicePatch;
        const invoice = applyInvoicePatch(state, invoiceID, patch);
        await fulfillJson(route, invoice);
        return;
      }
    }
    const invoiceSendMatch = path.match(
      /^\/api\/invoicing\/invoices\/([^/]+)\/send$/,
    );
    if (invoiceSendMatch && request.method() === "POST") {
      const invoiceID = decodeURIComponent(invoiceSendMatch[1]);
      const invoice = sendInvoice(state, invoiceID);
      await fulfillJson(route, {
        invoice,
        locked_rate: lockedRate(),
        number: invoice.number,
      });
      return;
    }
    if (path === "/api/banking/accounts") {
      await fulfillJson(route, { accounts: bankingAccounts(state) });
      return;
    }
    if (path === "/api/banking/review") {
      await fulfillJson(route, bankingReviewQueue(state));
      return;
    }
    if (path === "/api/banking/recent") {
      await fulfillJson(route, { transactions: state.recentBanking });
      return;
    }
    if (
      path === "/api/banking/accounts/1/import" &&
      request.method() === "POST"
    ) {
      state.paymentImported = true;
      await fulfillJson(route, {
        account_id: 1,
        batch_id: 1,
        duplicates: 0,
        filename: "contoso-payment.csv",
        imported_at: "2026-07-06T11:00:00Z",
        new: 1,
        total: 1,
      });
      return;
    }
    if (
      path === "/api/banking/transactions/101/confirm" &&
      request.method() === "POST"
    ) {
      state.paymentConfirmed = true;
      const invoice = state.invoices.get("inv-july-retainer");
      if (invoice) {
        state.invoices.set(invoice.id, {
          ...invoice,
          settled_amount: invoiceMoney(4_500_00, "EUR"),
          settled_date: "2026-07-06T00:00:00Z",
          settlement_txn_ref: "bank:101",
          status: "paid",
        });
      }
      const transaction = bankingPaymentTransaction("reconciled");
      state.recentBanking = [
        {
          actor: "reconciliation-command",
          reconciled_at: "2026-07-06T11:05:00Z",
          transaction,
        },
      ];
      await fulfillJson(route, {
        kind: "match",
        realised_fx_amount: bankMoney(4_500, "GBP"),
        transaction,
      });
      return;
    }
    if (path === "/api/dla/balance") {
      await fulfillJson(route, dlaBalance(state));
      return;
    }
    if (path === "/api/dla/ledger") {
      await fulfillJson(route, dlaLedger(state));
      return;
    }
    if (path === "/api/dividends/headroom") {
      await fulfillJson(route, dividendHeadroom(state));
      return;
    }
    if (path === "/api/dividends/history") {
      await fulfillJson(route, { declarations: state.dividends });
      return;
    }
    if (path === "/api/dividends/validate" && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}") as {
        amount: Money;
      };
      await fulfillJson(route, dividendValidation(state, body.amount.amount));
      return;
    }
    if (path === "/api/dividends/declare" && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}") as {
        amount: Money;
      };
      const declaration = dividendDeclaration(state, body.amount.amount);
      state.dividends = [declaration, ...state.dividends];
      await fulfillJson(route, declaration, 201);
      return;
    }
    const dividendPrintMatch = path.match(
      /^\/api\/dividends\/declarations\/([^/]+)\/print$/,
    );
    if (dividendPrintMatch) {
      const id = decodeURIComponent(dividendPrintMatch[1]);
      const declaration = state.dividends.find((item) => item.id === id);
      await fulfillJson(
        route,
        declaration ? { declaration } : notFound(),
        declaration ? 200 : 404,
      );
      return;
    }
    if (path === "/api/reports/pl") {
      await fulfillJson(route, reportsPL(url));
      return;
    }
    if (path === "/api/reports/vat") {
      await fulfillJson(route, reportsVAT());
      return;
    }
    if (path === "/api/reports/calendar") {
      await fulfillJson(route, reportsCalendar(state));
      return;
    }
    if (path === "/api/reports/export") {
      await route.fulfill({
        body: "PK walkthrough export",
        contentType: "application/zip",
        headers: {
          "Content-Disposition":
            "attachment; filename=ledgerly-export-2026-07-01_2026-09-30.zip",
        },
        status: 200,
      });
      return;
    }
    if (path === "/api/advisor/insights" && request.method() === "GET") {
      const surface = url.searchParams.get("surface");
      await fulfillJson(route, { insights: advisorInsights(state, surface) });
      return;
    }
    if (
      path.startsWith("/api/advisor/insights/") &&
      path.endsWith("/dismiss") &&
      request.method() === "POST"
    ) {
      state.dismissedAdvisorKeys.add(
        decodeURIComponent(path.split("/").at(-2) ?? ""),
      );
      await fulfillJson(route, {}, 204);
      return;
    }
    if (path === "/api/advisor/refresh" && request.method() === "POST") {
      await fulfillJson(route, advisorRefreshResponse(state));
      return;
    }

    await fulfillJson(route, notFound(), 404);
  });
}

type Currency = "EUR" | "GBP";
type InvoiceStatus = "draft" | "sent" | "paid" | "overdue";

type Money = {
  amount: number;
  currency: Currency;
};

type BankMoney = {
  amount_minor: number;
  currency: Currency;
};

type MinorMoney = {
  amount_minor: number;
  currency: Currency;
};

type Client = {
  address: {
    country: string;
    line1: string;
    line2: string;
    locality: string;
    postal_code: string;
    region: string;
  };
  archived_at: string | null;
  created_at: string;
  day_rate: MinorMoney | null;
  default_currency: Currency;
  email: string | null;
  id: string;
  name: string;
  retainer_amount: MinorMoney | null;
  terms_days: 14 | 30;
  vat_number: string | null;
  vat_treatment: "domestic" | "reverse-charge-eu-b2b";
};

type ClientRequest = Omit<Client, "archived_at" | "created_at" | "id">;

type InvoiceLine = {
  description: string;
  id: string;
  invoice_id: string;
  line_total: Money;
  position: number;
  qty: string;
  unit_price: Money;
};

type Invoice = {
  client_id: string;
  created_at: string;
  currency: Currency;
  due_date: string;
  id: string;
  issue_date: string;
  lines: InvoiceLine[];
  lock_id: number | null;
  number: string | null;
  pdf_asset: string | null;
  sent_at: string | null;
  settled_amount: Money | null;
  settled_date: string | null;
  settlement_txn_ref: string | null;
  status: InvoiceStatus;
  totals: {
    approx_gbp: {
      amount: Money;
      as_of: string;
      locked: boolean;
      rate: {
        from: Currency;
        rate_date: string;
        source: string;
        to: Currency;
        value: string;
      };
    } | null;
    subtotal: Money;
    total: Money;
    vat: Money;
  };
  updated_at: string;
  vat_treatment: "domestic" | "reverse-charge-eu-b2b";
};

type InvoicePatch = {
  client_id?: string;
  currency?: Currency;
  due_date?: string;
  issue_date?: string;
  lines?: Array<{
    description: string;
    id: string;
    qty: string;
    unit_price: Money;
  }>;
  vat_treatment?: "domestic" | "reverse-charge-eu-b2b";
};

type BankingTransaction = {
  account_id: number;
  amount: BankMoney;
  created_at: string;
  date: string;
  id: number;
  import_batch_id: number;
  payee: string;
  provider_meta: Record<string, unknown>;
  reference: string;
  state: "suggested" | "reconciled";
};

type DividendDeclaration = ReturnType<typeof dividendDeclaration>;

type WalkthroughState = {
  authenticated: boolean;
  clients: Client[];
  dismissedAdvisorKeys: Set<string>;
  dividends: DividendDeclaration[];
  invoices: Map<string, Invoice>;
  now: Date;
  paymentConfirmed: boolean;
  paymentImported: boolean;
  recentBanking: Array<{
    actor: string;
    reconciled_at: string;
    transaction: BankingTransaction;
  }>;
};

function walkthroughState(): WalkthroughState {
  return {
    authenticated: false,
    clients: [],
    dismissedAdvisorKeys: new Set(),
    dividends: [],
    invoices: new Map(),
    now: new Date("2026-07-06T09:00:00Z"),
    paymentConfirmed: false,
    paymentImported: false,
    recentBanking: [],
  };
}

function currentUser() {
  return {
    created_at: "2026-07-05T12:00:00Z",
    email: "owner@example.com",
    id: 1,
    name: "N. Meyer",
  };
}

function identityProfile() {
  return {
    bank_details: {
      bank_name: "Revolut",
      bic: "REVOGB21",
      iban: "GB00REVO00000000000000",
    },
    company_number: "137792C",
    incorporation_date: "2024-04-03",
    legal_name: "NPM Limited",
    logo_asset_id: null,
    logo_asset_url: null,
    registered_office: {
      country: "IM",
      line1: "18 Athol St",
      line2: "",
      locality: "Douglas",
      postal_code: "IM1 1JA",
      region: "",
    },
    shareholders: [{ class: "ordinary GBP1", name: "N. Meyer", shares: 100 }],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

function clientFromRequest(request: ClientRequest): Client {
  return {
    ...request,
    archived_at: null,
    created_at: "2026-07-06T09:30:00Z",
    id: "client-contoso",
  };
}

function draftInvoice(clientID: string): Invoice {
  return {
    client_id: clientID,
    created_at: "2026-07-06T10:00:00Z",
    currency: "EUR",
    due_date: "2026-07-20T00:00:00Z",
    id: "inv-july-retainer",
    issue_date: "2026-07-06T00:00:00Z",
    lines: [],
    lock_id: null,
    number: null,
    pdf_asset: null,
    sent_at: null,
    settled_amount: null,
    settled_date: null,
    settlement_txn_ref: null,
    status: "draft",
    totals: invoiceTotals(0, false),
    updated_at: "2026-07-06T10:00:00Z",
    vat_treatment: "reverse-charge-eu-b2b",
  };
}

function applyInvoicePatch(
  state: WalkthroughState,
  invoiceID: string,
  patch: InvoicePatch,
) {
  const current =
    state.invoices.get(invoiceID) ?? draftInvoice("client-contoso");
  const amount =
    patch.lines?.reduce(
      (sum, line) => sum + line.unit_price.amount * Number(line.qty),
      0,
    ) ?? current.totals.total.amount;
  const invoice: Invoice = {
    ...current,
    client_id: patch.client_id ?? current.client_id,
    currency: patch.currency ?? current.currency,
    due_date: patch.due_date ? `${patch.due_date}T00:00:00Z` : current.due_date,
    issue_date: patch.issue_date
      ? `${patch.issue_date}T00:00:00Z`
      : current.issue_date,
    lines:
      patch.lines?.map((line, index) => ({
        ...line,
        invoice_id: current.id,
        line_total: line.unit_price,
        position: index + 1,
      })) ?? current.lines,
    totals: invoiceTotals(amount, false),
    updated_at: "2026-07-06T10:01:00Z",
    vat_treatment: patch.vat_treatment ?? current.vat_treatment,
  };
  state.invoices.set(invoice.id, invoice);
  return invoice;
}

function sendInvoice(state: WalkthroughState, invoiceID: string) {
  const current =
    state.invoices.get(invoiceID) ?? draftInvoice("client-contoso");
  const invoice: Invoice = {
    ...current,
    lock_id: 701,
    number: "INV-2026-0001",
    sent_at: "2026-07-06T10:10:00Z",
    status: "sent",
    totals: invoiceTotals(current.totals.total.amount, true),
    updated_at: "2026-07-06T10:10:00Z",
  };
  state.invoices.set(invoice.id, invoice);
  return invoice;
}

function invoiceTotals(amount: number, locked: boolean): Invoice["totals"] {
  const gbpAmount = Math.round(amount * Number(expected.lockedRate));
  return {
    approx_gbp: amount
      ? {
          amount: invoiceMoney(gbpAmount, "GBP"),
          as_of: "2026-07-06T10:00:00Z",
          locked,
          rate: {
            from: "EUR",
            rate_date: "2026-07-06",
            source: "ECB",
            to: "GBP",
            value: expected.lockedRate,
          },
        }
      : null,
    subtotal: invoiceMoney(amount, "EUR"),
    total: invoiceMoney(amount, "EUR"),
    vat: invoiceMoney(0, "EUR"),
  };
}

function lockedRate() {
  return {
    from: "EUR",
    id: 701,
    rate: expected.lockedRate,
    rate_date: "2026-07-06",
    source: "ECB",
    to: "GBP",
  };
}

function dashboardSummary(state: WalkthroughState) {
  const sentInvoice = [...state.invoices.values()].find(
    (invoice) => invoice.status === "sent" || invoice.status === "paid",
  );
  const outstandingAmount =
    sentInvoice && sentInvoice.status !== "paid"
      ? sentInvoice.totals.total.amount
      : 0;
  return {
    cash: {
      accounts: [
        {
          currency: "EUR",
          gbp_balance: dashboardMoney(
            state.paymentConfirmed ? 388_530 : 0,
            "GBP",
          ),
          id: 1,
          ledger_account_code: "1001-cash-eur",
          name: "Revolut EUR",
          native_balance: dashboardMoney(
            state.paymentConfirmed ? 450_000 : 0,
            "EUR",
          ),
          provider: "revolut",
        },
      ],
      total_gbp: dashboardMoney(state.paymentConfirmed ? 388_530 : 0, "GBP"),
    },
    dividendHeadroom: {
      available: dashboardMoney(dividendAvailable(state), "GBP"),
      distributable: dividendAvailable(state) > 0,
    },
    dla: {
      balance: dashboardMoney(dividendsDeclared(state), "GBP"),
      status: "credit",
    },
    errors: [],
    greeting: {
      trading_name: "NPM Limited",
      user_name: "N. Meyer",
    },
    outstanding: {
      earliest_due_date: outstandingAmount ? "2026-07-20" : null,
      total_gbp: dashboardMoney(
        outstandingAmount ? Math.round(outstandingAmount * 0.8534) : 0,
        "GBP",
      ),
      totals: outstandingAmount
        ? [dashboardMoney(outstandingAmount, "EUR")]
        : [],
    },
    rate: {
      fetched_at: "2026-07-06T08:30:00Z",
      from: "EUR",
      rate: expected.lockedRate,
      rate_date: "2026-07-06",
      source: "ECB daily",
      to: "GBP",
    },
    recentInvoices: sentInvoice
      ? [
          {
            amount: dashboardMoney(sentInvoice.totals.total.amount, "EUR"),
            client: "Contoso GmbH",
            days_overdue: null,
            id: sentInvoice.id,
            number: sentInvoice.number,
            status: sentInvoice.status,
          },
        ]
      : [],
    toReconcile: {
      accounts: [
        {
          currency: "EUR",
          id: 1,
          ledger_account_code: "1001-cash-eur",
          name: "Revolut EUR",
          unreconciled_count:
            state.paymentImported && !state.paymentConfirmed ? 1 : 0,
        },
      ],
      review_queue:
        state.paymentImported && !state.paymentConfirmed
          ? [
              {
                amount: dashboardMoney(450_000, "EUR"),
                confidence: 0.98,
                kind: "invoice-match",
                payee: "CONTOSO GMBH SEPA",
              },
            ]
          : [],
    },
  };
}

function invoicesResponse(state: WalkthroughState) {
  const invoices = [...state.invoices.values()].map((invoice) => ({
    client_id: invoice.client_id,
    client_name: "Contoso GmbH",
    created_at: invoice.created_at,
    currency: invoice.currency,
    days_overdue: 0,
    due_date: invoice.due_date,
    id: invoice.id,
    issue_date: invoice.issue_date,
    number: invoice.number,
    status: invoice.status,
    totals: invoice.totals,
    updated_at: invoice.updated_at,
  }));
  return {
    counts: (["draft", "sent", "paid", "overdue"] as const).map((status) => ({
      count: invoices.filter((invoice) => invoice.status === status).length,
      status,
    })),
    invoices,
    limit: 50,
    offset: 0,
    total_count: invoices.length,
    totals: {
      subtotals: invoices.length ? [invoiceMoney(450_000, "EUR")] : [],
      total_gbp: invoiceMoney(
        invoices.length === 0 ? 0 : state.paymentConfirmed ? 388_530 : 384_030,
        "GBP",
      ),
    },
  };
}

function bankingAccounts(state: WalkthroughState) {
  return [
    {
      created_at: "2026-07-01T09:00:00Z",
      currency: "EUR",
      id: 1,
      ledger_account_code: "1001-cash-eur",
      name: "Revolut EUR",
      provider: "revolut",
      unreconciled_count:
        state.paymentImported && !state.paymentConfirmed ? 1 : 0,
    },
  ];
}

function bankingReviewQueue(state: WalkthroughState) {
  return {
    matches:
      state.paymentImported && !state.paymentConfirmed
        ? [
            {
              confidence: 0.98,
              explanation: "Amount, client name, and invoice reference align.",
              kind: "match",
              suggestion_id: 9001,
              target: {
                client: "Contoso GmbH",
                id: "inv-july-retainer",
                invoice_number: "INV-2026-0001",
                type: "invoice",
              },
              transaction: bankingPaymentTransaction("suggested"),
            },
          ]
        : [],
    rules: [],
    suggestions: [],
  };
}

function bankingPaymentTransaction(
  transactionState: BankingTransaction["state"],
): BankingTransaction {
  return {
    account_id: 1,
    amount: bankMoney(450_000, "EUR"),
    created_at: "2026-07-06T11:00:00Z",
    date: "2026-07-06",
    id: 101,
    import_batch_id: 1,
    payee: "CONTOSO GMBH SEPA",
    provider_meta: {},
    reference: "INV-2026-0001",
    state: transactionState,
  };
}

function advisorInsights(state: WalkthroughState, surface: string | null) {
  if (surface !== "dashboard" || state.dismissedAdvisorKeys.has("vat-window")) {
    return [];
  }
  if (state.now < new Date("2026-07-20T00:00:00Z")) {
    return [];
  }
  return [
    {
      bindings: { due_date: "2026-07-30" },
      created_at: state.now.toISOString(),
      cta: { action: "reports.openFilingCalendar", label: "Open reports" },
      key: "vat-window",
      rendered_text: "VAT return is due 30 Jul. Review the filing calendar.",
      rule_id: "filing_deadline_window",
      severity: "amber",
      surfaces: ["dashboard", "reports"],
    },
  ];
}

function advisorRefreshResponse(state: WalkthroughState) {
  return {
    run: {
      duration_ms: 1,
      finished_at: state.now.toISOString(),
      id: 1,
      insights_created: advisorInsights(state, "dashboard").length,
      insights_resolved: 0,
      insights_superseded: 0,
      started_at: state.now.toISOString(),
      trigger: "manual.RefreshNow",
      warnings: [],
    },
  };
}

function dividendHeadroom(state: WalkthroughState) {
  const available = dividendAvailable(state);
  return {
    as_of: state.now.toISOString(),
    available: invoiceMoney(available, "GBP"),
    distributable: available >= 0,
    financial_year: "2026-27",
    lines: [
      { amount: invoiceMoney(388_530, "GBP"), label: "Profit YTD" },
      {
        amount: invoiceMoney(-dividendsDeclared(state), "GBP"),
        label: "Dividends already declared YTD",
      },
      {
        amount: invoiceMoney(available, "GBP"),
        label: "Available to distribute",
      },
    ],
  };
}

function dividendValidation(state: WalkthroughState, amount: number) {
  const headroom = dividendHeadroom(state);
  const withinHeadroom = amount <= headroom.available.amount;
  return {
    amount: invoiceMoney(amount, "GBP"),
    distributable: headroom.distributable,
    distributable_total: headroom.available,
    headroom,
    personal_tax: {
      marginal: invoiceMoney(Math.round(amount * 0.1), "GBP"),
      message: `set aside personally ${expected.dividendPersonalTax.slice(
        "set aside personally ".length,
      )}`,
      prior_ytd: invoiceMoney(0, "GBP"),
      tax_year: "2026-27",
      with_dividend: invoiceMoney(amount, "GBP"),
    },
    withholding: {
      applies: false,
      informational: true,
      policy: "none",
      tax_year: "2026-27",
    },
    within_headroom: withinHeadroom,
  };
}

function dividendDeclaration(state: WalkthroughState, amount: number) {
  return {
    amount: invoiceMoney(amount, "GBP"),
    company_snapshot: {
      company_number: "137792C",
      director_name: "N. Meyer",
      legal_name: "NPM Limited",
      registered_office: identityProfile().registered_office,
      trading_name: "NPM Limited",
    },
    created_at: state.now.toISOString(),
    declared_date: state.now.toISOString().slice(0, 10),
    headroom_snapshot: dividendHeadroom(state),
    id: `dividend-${state.dividends.length + 1}`,
    minutes_asset: "minutes-asset",
    per_share: invoiceMoney(amount / 100, "GBP"),
    shareholder_name: "N. Meyer",
    shareholder_snapshot: {
      class: "ordinary GBP1",
      name: "N. Meyer",
      shares: 100,
    },
    shares: 100,
    voucher_asset: "voucher-asset",
    withholding_snapshot: {
      note: "No dividend withholding tax is deducted under the active jurisdiction pack.",
      policy: "none",
      tax_year: "2026-27",
    },
  };
}

function dlaBalance(state: WalkthroughState) {
  const balance = dividendsDeclared(state);
  return {
    balance: bankMoney(balance, "GBP"),
    policy: dlaPolicy(),
    status: "credit",
    suggested_clearance: null,
  };
}

function dlaLedger(state: WalkthroughState) {
  return {
    entries: state.dividends.map((declaration, index) => ({
      amount: bankMoney(declaration.amount.amount, "GBP"),
      balance_side: "CR",
      created_at: declaration.created_at,
      date: declaration.declared_date,
      description: "Dividend declared",
      drawn: bankMoney(0, "GBP"),
      id: index + 1,
      kind: "expense-owed",
      owed_to_you: bankMoney(declaration.amount.amount, "GBP"),
      running_balance: bankMoney(dividendsDeclared(state), "GBP"),
      source_ref: `dividend:${declaration.id}`,
    })),
    next_cursor: null,
  };
}

function dlaPolicy() {
  return {
    bik_warning_key: "benefit_in_kind_interest_free",
    credit_explainer_template:
      "You can repay yourself up to {{ balance }} at any time with no tax consequence.",
    credit_status_text: "In credit — tax-free to withdraw",
    overdrawn_warning_template:
      "Your loan account is {{ balance }} overdrawn. Clear it with a dividend.",
    remedy: "clear_with_dividend",
    s455_charge: false,
  };
}

function reportsPL(url: URL) {
  return {
    corporate_tax: {
      amount: reportMoney(0),
      label: "IoM income tax at 0%",
      rate: "0.0",
      tax_year: "2026-27",
    },
    expense_total: reportMoney(0),
    expenses: [],
    income: [
      {
        amount: reportMoney(384_030),
        client_id: "client-contoso",
        client_name: "Contoso GmbH",
        currency: "EUR",
        label: "Consulting income - Contoso GmbH (EUR)",
      },
    ],
    income_total: reportMoney(388_530),
    net_profit: reportMoney(388_530),
    period: {
      from: url.searchParams.get("from") ?? "2026-07-01",
      to: url.searchParams.get("to") ?? "2026-09-30",
    },
    profit_before_tax: reportMoney(388_530),
    realised_fx_gains: {
      amount: reportMoney(4_500),
      label: "Realised FX gains",
    },
    tax_year: "2026-27",
  };
}

function reportsVAT() {
  return {
    box1: reportMoney(0),
    box4: reportMoney(0),
    box6: reportMoney(450_000),
    net_position: reportMoney(0),
    period: { from: "2026-07-01", to: "2026-09-30" },
  };
}

function reportsCalendar(state: WalkthroughState) {
  const dueSoon = state.now >= new Date("2026-07-20T00:00:00Z");
  return {
    filings: [
      {
        authority: "Isle of Man Customs & Excise",
        days_until: dueSoon ? 6 : 24,
        due_date: "2026-07-30",
        key: "vat_return",
        label: "VAT return",
        status: dueSoon ? "due-soon" : "upcoming",
      },
      {
        authority: "IoM Companies Registry",
        days_until: 38,
        due_date: "2026-08-14",
        key: "annual_return",
        label: "Annual return",
        status: "upcoming",
      },
    ],
  };
}

function dividendsDeclared(state: WalkthroughState) {
  return state.dividends.reduce(
    (sum, declaration) => sum + declaration.amount.amount,
    0,
  );
}

function dividendAvailable(state: WalkthroughState) {
  return 300_000 - dividendsDeclared(state);
}

function invoiceMoney(amount: number, currency: Currency): Money {
  return { amount, currency };
}

function dashboardMoney(amount: number, currency: Currency) {
  return { amount, currency };
}

function bankMoney(amount_minor: number, currency: Currency): BankMoney {
  return { amount_minor, currency };
}

function reportMoney(amount_minor: number) {
  return { amount_minor, currency: "GBP" };
}

function notFound() {
  return { status: 404, title: "Not Found", type: "about:blank" };
}

async function fulfillJson(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    body: status === 204 ? "" : JSON.stringify(body),
    contentType: "application/json",
    status,
  });
}
