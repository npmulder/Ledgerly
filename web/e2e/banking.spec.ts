import { expect, test, type Page, type Route } from "@playwright/test";

import type {
  BankingAccount,
  BankingCreateAccountRequest,
  BankingMoney,
  BankingRecentTransaction,
  BankingReceipt,
  BankingReviewCard,
  BankingReviewQueue,
} from "@/api/banking";
import type { LedgerAccount } from "@/api/ledger";

test("creates bank accounts from the Banking screen", async ({ page }) => {
  const state = bankingState([]);
  await mockBankingApi(page, state);

  await page.goto("/banking");

  await expect(page.getByText("No bank accounts")).toBeVisible();
  await expect(page.getByRole("button", { name: "Import CSV" })).toBeDisabled();

  await page.getByRole("button", { name: "Add account" }).click();
  await page.getByLabel("Account name").fill("Operating GBP");
  await page.getByRole("button", { name: "Create account" }).click();

  await expect(page.getByRole("status")).toContainText(
    "Created Operating GBP. CSV import ready.",
  );
  await expect(
    page.getByRole("button", { name: /Revolut Business Operating GBP/ }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("button", { name: "Import CSV" })).toBeEnabled();

  await page.getByRole("button", { name: "Add account" }).click();
  await page.getByLabel("Account name").fill("Operating EUR");
  await page.getByLabel("Currency").selectOption("EUR");
  await page.getByRole("button", { name: "Create account" }).click();

  await expect(page.getByRole("status")).toContainText(
    "Created Operating EUR. CSV import ready.",
  );
  await expect(
    page.getByRole("button", { name: /Revolut Business Operating EUR/ }),
  ).toHaveAttribute("aria-pressed", "true");

  await page.getByRole("button", { name: "Add account" }).click();
  await page.getByLabel("Account name").fill("Operating EUR");
  await page.getByLabel("Currency").selectOption("EUR");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByRole("alert")).toContainText(
    'A Revolut Business EUR account named "Operating EUR" already exists.',
  );

  await page.getByRole("button", { name: "Cancel" }).click();
  const chooserPromise = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Import CSV" }).click();
  const chooser = await chooserPromise;
  await chooser.setFiles({
    buffer: Buffer.from(
      "Date,Description,Amount,Currency\n2026-07-06,Seed,1,EUR\n",
    ),
    mimeType: "text/csv",
    name: "created-account.csv",
  });

  await expect(page.getByRole("status")).toContainText(
    "created-account.csv: 0 new, 0 duplicates",
  );
  await page.screenshot({
    fullPage: true,
    path: "test-results/banking-screen-06-account-create.png",
  });
});

test("imports CSV and reconciles match, DLA, and recode cards", async ({
  page,
}) => {
  const state = bankingState();
  await mockBankingApi(page, state);

  await page.goto("/banking");

  await expect(page.getByRole("heading", { name: "Banking" })).toBeVisible();
  await expect(
    page.getByRole("button", { name: /Revolut Business Revolut GBP/ }),
  ).toContainText("0");

  const chooserPromise = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Import CSV" }).click();
  const chooser = await chooserPromise;
  await chooser.setFiles({
    buffer: Buffer.from(
      "Date,Description,Amount,Currency\n2026-07-06,Seed,1,GBP\n",
    ),
    mimeType: "text/csv",
    name: "banking-fixture.csv",
  });

  await expect(page.getByRole("status")).toContainText(
    "banking-fixture.csv: 3 new, 1 duplicates",
  );
  await expect(page.getByText("Invoice match")).toBeVisible();
  await expect(page.getByText("DLA suggestion")).toBeVisible();
  await expect(
    page.getByRole("button", { name: /Revolut Business Revolut GBP/ }),
  ).toContainText("2");
  await expect(
    page.getByRole("button", { name: /Revolut Business Revolut EUR/ }),
  ).toContainText("1");

  const receiptChooserPromise = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Attach receipt" }).first().click();
  const receiptChooser = await receiptChooserPromise;
  await receiptChooser.setFiles({
    buffer: Buffer.from("%PDF-1.4\n% receipt fixture\n%%EOF\n"),
    mimeType: "application/pdf",
    name: "receipt.pdf",
  });
  await expect(page.getByRole("status")).toContainText("Attached receipt.pdf");
  await expect(
    page.getByRole("link", { name: "Preview receipt" }),
  ).toBeVisible();
  await page.screenshot({
    fullPage: true,
    path: "test-results/banking-screen-05-populated.png",
  });

  await page.getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByRole("status")).toContainText(
    "auto-posted FX gain £45.00",
  );
  await expect(page.getByText("Invoice match")).toBeHidden();
  await expect(page.getByText("CONTOSO GMBH SEPA")).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Preview receipt" }),
  ).toBeVisible();
  await page.screenshot({
    fullPage: true,
    path: "test-results/banking-screen-05-receipt-recent.png",
  });

  await page.getByRole("button", { name: "File to DLA" }).click();
  await expect(page.getByRole("status")).toContainText(
    "Filed to DLA £2,400.00",
  );
  await expect(page.getByText("DLA suggestion")).toBeHidden();

  await page
    .getByRole("button", { name: /Revolut Business Revolut EUR/ })
    .click();
  await expect(page.getByText("Payee rule", { exact: true })).toBeVisible();
  await page.getByText("Recode ▾").click();
  await page.getByLabel("Rule recode account").selectOption("5020-travel");
  await page.getByRole("button", { name: "Recode selected" }).click();

  await expect(page.getByRole("status")).toContainText(
    "Recoded to Travel; rule applied 12 times",
  );
  await expect(page.getByText("Payee rule", { exact: true })).toBeHidden();
  await expect(page.getByText("All caught up…").first()).toBeVisible();
  await page.screenshot({
    fullPage: true,
    path: "test-results/banking-screen-05-empty.png",
  });
  expect(state.recodeRequests).toEqual([{ account_code: "5020-travel" }]);
});

test("shows draft invoice matches as send-and-allocate cards", async ({
  page,
}) => {
  const state = bankingState();
  state.queue = {
    matches: [
      reviewCard({
        confidence: 0.85,
        explanation:
          "85% draft invoice match for draft invoice inv_draft: confirming will send the invoice before allocating payment; exact native amount, payee resembles client",
        kind: "match",
        suggestion_id: 9011,
        target: {
          client: "Contoso GmbH",
          id: "inv_draft",
          invoice_status: "draft",
          type: "invoice",
        },
        transaction: transactionFixture({
          payee: "CONTOSO GMBH SEPA",
          reference: "bank transfer",
        }),
      }),
    ],
    rules: [],
    suggestions: [],
  };
  await mockBankingApi(page, state);

  await page.goto("/banking");

  await expect(
    page.getByText("Draft invoice match", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Draft invoice", { exact: true })).toBeVisible();
  await expect(
    page.getByText(/confirming will send the invoice before allocating/),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Send + allocate" }),
  ).toBeVisible();
  await page.screenshot({
    fullPage: true,
    path: "test-results/banking-draft-invoice-match.png",
  });
});

async function mockBankingApi(page: Page, state: BankingState) {
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
      await fulfillJson(route, {
        logo_asset_url: null,
        trading_name: "NPM Limited",
      });
      return;
    }
    if (path === "/api/ledger/accounts" && request.method() === "GET") {
      await fulfillJson(route, { accounts: state.expenseAccounts });
      return;
    }
    if (path === "/api/ledger/accounts" && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}") as {
        code: string;
        name: string;
      };
      const account = ledgerAccount({ code: body.code, name: body.name });
      state.expenseAccounts = [...state.expenseAccounts, account];
      await fulfillJson(route, account, 201);
      return;
    }
    if (path === "/api/banking/accounts" && request.method() === "GET") {
      await fulfillJson(route, { accounts: state.accounts });
      return;
    }
    if (path === "/api/banking/accounts" && request.method() === "POST") {
      const body = JSON.parse(
        request.postData() ?? "{}",
      ) as BankingCreateAccountRequest;
      const existing = state.accounts.find(
        (account) =>
          account.provider === body.provider &&
          account.currency === body.currency &&
          account.name === body.name,
      );
      if (existing) {
        await fulfillJson(route, existing, 201);
        return;
      }
      const nextID =
        Math.max(0, ...state.accounts.map((account) => account.id)) + 1;
      const account = bankingAccount({
        currency: body.currency,
        id: nextID,
        ledger_account_code: `1000-cash-revolut-${body.currency.toLowerCase()}`,
        name: body.name,
        provider: body.provider,
      });
      state.accounts = [...state.accounts, account];
      await fulfillJson(route, account, 201);
      return;
    }
    if (path === "/api/banking/review") {
      await fulfillJson(route, state.queue);
      return;
    }
    if (path === "/api/banking/recent") {
      await fulfillJson(route, { transactions: state.recent });
      return;
    }
    const importMatch = path.match(/^\/api\/banking\/accounts\/(\d+)\/import$/);
    if (importMatch && request.method() === "POST") {
      const accountID = Number(importMatch[1]);
      state.imports += 1;
      if (accountID === 1) {
        state.queue = reviewQueueFixture();
      }
      await fulfillJson(route, {
        account_id: accountID,
        batch_id: 301,
        duplicates: accountID === 1 ? 1 : 0,
        filename:
          accountID === 1 ? "banking-fixture.csv" : "created-account.csv",
        imported_at: "2026-07-06T10:00:00Z",
        new: accountID === 1 ? 3 : 0,
        total: accountID === 1 ? 4 : 0,
      });
      return;
    }
    if (
      path === "/api/banking/transactions/101/confirm" &&
      request.method() === "POST"
    ) {
      const card = removeCard(state, 101);
      if (card) {
        addRecent(state, card);
      }
      await fulfillJson(route, {
        kind: "match",
        realised_fx_amount: money(4500, "GBP"),
        transaction: card?.transaction,
      });
      return;
    }
    if (
      path === "/api/banking/transactions/101/receipt" &&
      request.method() === "PUT"
    ) {
      const receipt = receiptFixture(101);
      const card = state.queue.matches.find(
        (item) => item.transaction.id === 101,
      );
      if (card) {
        card.transaction.receipt = receipt;
      }
      await fulfillJson(route, receipt);
      return;
    }
    if (
      path === "/api/banking/transactions/101/receipt" &&
      request.method() === "GET"
    ) {
      await route.fulfill({
        body: "%PDF-1.4\n% receipt fixture\n%%EOF\n",
        contentType: "application/pdf",
        status: 200,
      });
      return;
    }
    if (
      path === "/api/banking/transactions/102/file-dla" &&
      request.method() === "POST"
    ) {
      const card = removeCard(state, 102);
      if (card) {
        addRecent(state, card);
      }
      await fulfillJson(route, {
        amount_gbp: money(240000, "GBP"),
        kind: "suggestion",
        transaction: card?.transaction,
      });
      return;
    }
    if (
      path === "/api/banking/transactions/103/recode" &&
      request.method() === "POST"
    ) {
      const body = JSON.parse(request.postData() ?? "{}") as {
        account_code: string;
      };
      state.recodeRequests.push(body);
      const card = removeCard(state, 103);
      if (card) {
        addRecent(state, card);
      }
      await fulfillJson(route, {
        kind: "rule",
        rule: {
          account_code: body.account_code,
          created_at: "2026-07-06T10:10:00Z",
          created_from: "recode",
          id: 700,
          last_applied_at: "2026-07-06T10:10:00Z",
          match_mode: "contains",
          matcher: "HETZNER ONLINE",
          times_applied: 12,
        },
        transaction: card?.transaction,
      });
      return;
    }

    await fulfillJson(
      route,
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
  });
}

type BankingState = {
  accounts: BankingAccount[];
  expenseAccounts: LedgerAccount[];
  imports: number;
  queue: BankingReviewQueue;
  recent: BankingRecentTransaction[];
  recodeRequests: Array<{ account_code: string }>;
};

function bankingState(
  accounts: BankingAccount[] = accountsFixture(),
): BankingState {
  return {
    accounts,
    expenseAccounts: expenseAccountsFixture(),
    imports: 0,
    queue: { matches: [], rules: [], suggestions: [] },
    recent: [],
    recodeRequests: [],
  };
}

function accountsFixture(): BankingAccount[] {
  return [
    bankingAccount({
      currency: "GBP",
      id: 1,
      ledger_account_code: "1000-cash-gbp",
      name: "Revolut GBP",
    }),
    bankingAccount({
      currency: "EUR",
      id: 2,
      ledger_account_code: "1001-cash-eur",
      name: "Revolut EUR",
    }),
  ];
}

function bankingAccount(overrides: Partial<BankingAccount>): BankingAccount {
  return {
    created_at: "2026-07-01T09:00:00Z",
    currency: "GBP",
    id: 1,
    ledger_account_code: "1000-cash-gbp",
    name: "Revolut GBP",
    provider: "revolut",
    unreconciled_count: 0,
    ...overrides,
  };
}

function reviewQueueFixture(): BankingReviewQueue {
  return {
    matches: [
      reviewCard({
        confidence: 0.98,
        explanation: "Amount, client name, and invoice reference align.",
        kind: "match",
        suggestion_id: 9001,
        target: {
          client: "Contoso GmbH",
          id: "inv_2026_07",
          invoice_number: "INV-2026-07",
          invoice_status: "sent",
          type: "invoice",
        },
        transaction: transactionFixture({
          amount: money(450000, "EUR"),
          payee: "CONTOSO GMBH SEPA",
          reference: "INV-2026-07",
        }),
      }),
    ],
    rules: [
      reviewCard({
        confidence: 0.91,
        explanation: "Recurring payee matched the software rule.",
        kind: "rule",
        suggestion_id: 9003,
        target: {
          account_code: "5010-software",
          times_applied: 11,
          type: "account",
        },
        transaction: transactionFixture({
          account_id: 2,
          amount: money(-890, "EUR"),
          id: 103,
          payee: "HETZNER ONLINE",
          reference: "cloud hosting",
        }),
      }),
    ],
    suggestions: [
      reviewCard({
        confidence: 0.88,
        explanation: "Payee matches known director drawing pattern.",
        kind: "suggestion",
        suggestion_id: 9002,
        target: { id: "director-loan", type: "dla" },
        transaction: transactionFixture({
          amount: money(-240000, "GBP"),
          id: 102,
          payee: "TRANSFER TO N MEYER",
          reference: "director drawing",
        }),
      }),
    ],
  };
}

function reviewCard(card: BankingReviewCard): BankingReviewCard {
  return card;
}

function expenseAccountsFixture(): LedgerAccount[] {
  return [
    ledgerAccount({ code: "5000-fees", name: "Fees" }),
    ledgerAccount({ code: "5010-software", name: "Software" }),
    ledgerAccount({ code: "5020-travel", name: "Travel" }),
    ledgerAccount({ code: "5030-office", name: "Office" }),
  ];
}

function ledgerAccount(overrides: Partial<LedgerAccount>): LedgerAccount {
  return {
    code: "5010-software",
    created_at: "2026-07-06T10:00:00Z",
    currency: null,
    id: 5010,
    name: "Software",
    type: "expense",
    ...overrides,
  };
}

function transactionFixture(
  overrides: Partial<BankingReviewCard["transaction"]> = {},
): BankingReviewCard["transaction"] {
  return {
    account_id: 1,
    amount: money(450000, "EUR"),
    created_at: "2026-07-06T09:00:00Z",
    date: "2026-07-06",
    id: 101,
    import_batch_id: 77,
    payee: "CONTOSO GMBH SEPA",
    provider_meta: {},
    receipt: null,
    reference: "INV-2026-07",
    state: "suggested",
    ...overrides,
  };
}

function receiptFixture(transactionID: number): BankingReceipt {
  return {
    content_type: "application/pdf",
    filename: "receipt.pdf",
    size: 33,
    uploaded_at: "2026-07-06T10:03:00Z",
    url: `/api/banking/transactions/${transactionID}/receipt`,
  };
}

function removeCard(state: BankingState, transactionID: number) {
  const cards = [
    ...state.queue.matches,
    ...state.queue.suggestions,
    ...state.queue.rules,
  ];
  const card = cards.find((item) => item.transaction.id === transactionID);
  state.queue = {
    matches: state.queue.matches.filter(
      (item) => item.transaction.id !== transactionID,
    ),
    rules: state.queue.rules.filter(
      (item) => item.transaction.id !== transactionID,
    ),
    suggestions: state.queue.suggestions.filter(
      (item) => item.transaction.id !== transactionID,
    ),
  };
  return card;
}

function addRecent(state: BankingState, card: BankingReviewCard) {
  state.recent.unshift({
    actor: "reconciliation-command",
    reconciled_at: new Date("2026-07-06T10:05:00Z").toISOString(),
    transaction: { ...card.transaction, state: "reconciled" },
  });
}

function money(amountMinor: number, currency: string): BankingMoney {
  return { amount_minor: amountMinor, currency };
}

async function fulfillJson(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    body: JSON.stringify(body),
    contentType: "application/json",
    status,
  });
}
