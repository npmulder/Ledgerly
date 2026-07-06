import { expect, test, type Route } from "@playwright/test";

test("seeded DLA entries render running balances and repayment refreshes banner", async ({
  page,
}) => {
  const state = dlaState();
  await mockDLAApi(page, state);

  await page.goto("/dla");

  await expect(
    page.getByRole("heading", { name: "Director's loan · N. Meyer" }),
  ).toBeVisible();
  const ledger = page.getByLabel("DLA running ledger");
  await expect(ledger.getByText("£5,238.00 CR")).toBeVisible();
  await expect(ledger.getByText("£4,238.00 CR")).toBeVisible();
  await expect(ledger.getByText("£4,550.00 CR")).toBeVisible();
  await expect(ledger.getByText("£2,150.00 CR")).toBeVisible();

  await page.getByLabel("Amount").fill("100.00");
  await page.getByLabel("Description").fill("Director repayment");
  await page.getByRole("button", { name: "Record entry" }).last().click();

  await expect(page.getByRole("status")).toContainText("£2,250.00 CR");
  await expect(ledger.getByText("Director repayment")).toBeVisible();
});

test("overdrawn DLA CTA opens dividends with clearance amount", async ({
  page,
}) => {
  const state = dlaState({
    balance: {
      balance: { amount_minor: -300000, currency: "GBP" },
      policy: policyPayload(),
      status: "overdrawn",
      suggested_clearance: { amount_minor: 300000, currency: "GBP" },
    },
    ledger: {
      entries: [
        ledgerEntry({
          balance_side: "DR",
          date: "2026-07-01",
          description: "Director drawing",
          drawn: { amount_minor: 300000, currency: "GBP" },
          kind: "drawing",
          running_balance: { amount_minor: -300000, currency: "GBP" },
        }),
      ],
      next_cursor: null,
    },
  });
  await mockDLAApi(page, state);

  await page.goto("/dla");

  await expect(page.getByRole("status")).toContainText("£3,000.00 DR");
  await expect(
    page.getByText(/interest-free loan can create a taxable benefit in kind/),
  ).toBeVisible();

  await page.getByRole("button", { name: /Clear with dividend/ }).click();

  await expect(page).toHaveURL(/\/dividends\?amount=3000\.00$/);
});

async function mockDLAApi(
  page: Parameters<typeof test>[0]["page"],
  state: DLAState,
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
    if (path === "/api/dla/balance") {
      await fulfillJson(route, state.balance);
      return;
    }
    if (path === "/api/dla/ledger") {
      await fulfillJson(route, state.ledger);
      return;
    }
    if (path === "/api/dla/entries" && request.method() === "POST") {
      const entry = JSON.parse(request.postData() ?? "{}");
      state.balance = {
        ...state.balance,
        balance: {
          ...state.balance.balance,
          amount_minor:
            state.balance.balance.amount_minor + entry.amount.amount_minor,
        },
        status:
          state.balance.balance.amount_minor + entry.amount.amount_minor < 0
            ? "overdrawn"
            : "credit",
      };
      state.ledger = {
        entries: [
          ...state.ledger.entries,
          ledgerEntry({
            amount: entry.amount,
            balance_side: state.balance.balance.amount_minor < 0 ? "DR" : "CR",
            date: entry.date,
            description: entry.description,
            kind: entry.kind,
            owed_to_you: entry.amount,
            running_balance: state.balance.balance,
            source_ref: "manual:playwright-repayment",
          }),
        ],
        next_cursor: null,
      };
      await fulfillJson(
        route,
        { source_ref: "manual:playwright-repayment" },
        201,
      );
      return;
    }

    await fulfillJson(
      route,
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
  });
}

type DLAState = {
  balance: ReturnType<typeof creditBalance>;
  ledger: ReturnType<typeof creditLedger>;
};

function dlaState(overrides: Partial<DLAState> = {}): DLAState {
  return {
    balance: creditBalance(),
    ledger: creditLedger(),
    ...overrides,
  };
}

function creditBalance() {
  return {
    balance: { amount_minor: 215000, currency: "GBP" },
    policy: policyPayload(),
    status: "credit",
    suggested_clearance: null,
  };
}

function policyPayload() {
  return {
    bik_warning_key: "benefit_in_kind_interest_free",
    credit_explainer_template:
      "You can repay yourself up to {{ balance }} at any time with no tax consequence.",
    credit_status_text: "In credit — tax-free to withdraw",
    overdrawn_warning_template:
      "Your loan account is {{ balance }} overdrawn. The Isle of Man has no UK-style s455 charge, but an interest-free loan can create a taxable benefit in kind - charge interest at the official rate or clear it with a dividend.",
    remedy: "clear_with_dividend",
    s455_charge: false,
  };
}

function creditLedger() {
  return {
    entries: [
      ledgerEntry({
        date: "2026-05-12",
        description: "Company setup costs funded personally",
        kind: "expense-owed",
        owed_to_you: { amount_minor: 523800, currency: "GBP" },
        running_balance: { amount_minor: 523800, currency: "GBP" },
      }),
      ledgerEntry({
        date: "2026-06-01",
        description: "Loan repayment from company",
        drawn: { amount_minor: 100000, currency: "GBP" },
        id: 2,
        kind: "drawing",
        running_balance: { amount_minor: 423800, currency: "GBP" },
      }),
      ledgerEntry({
        date: "2026-06-14",
        description: "Expenses paid personally - flights, IOM Steam Packet",
        id: 3,
        kind: "expense-owed",
        owed_to_you: { amount_minor: 31200, currency: "GBP" },
        running_balance: { amount_minor: 455000, currency: "GBP" },
      }),
      ledgerEntry({
        date: "2026-06-30",
        description: "Drawing - bank transfer",
        drawn: { amount_minor: 240000, currency: "GBP" },
        id: 4,
        kind: "drawing",
        running_balance: { amount_minor: 215000, currency: "GBP" },
      }),
    ],
    next_cursor: null,
  };
}

function ledgerEntry(overrides: Record<string, unknown>) {
  return {
    amount: { amount_minor: 0, currency: "GBP" },
    balance_side: "CR",
    created_at: "2026-07-06T12:00:00Z",
    date: "2026-07-06",
    description: "DLA entry",
    drawn: { amount_minor: 0, currency: "GBP" },
    id: 1,
    kind: "expense-owed",
    owed_to_you: { amount_minor: 0, currency: "GBP" },
    running_balance: { amount_minor: 0, currency: "GBP" },
    source_ref: "manual:playwright",
    ...overrides,
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
