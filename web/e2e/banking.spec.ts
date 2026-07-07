import { expect, test, type Page, type Route } from "@playwright/test";

import type {
  BankingAccount,
  BankingInvoiceCandidate,
  BankingMoney,
  BankingRecentTransaction,
  BankingReceipt,
  BankingReviewCard,
  BankingReviewQueue,
  BankingTransaction,
} from "@/api/banking";

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
    if (path === "/api/ledger/accounts") {
      await fulfillJson(route, { accounts: expenseAccountsFixture() });
      return;
    }
    if (path === "/api/banking/accounts") {
      await fulfillJson(route, { accounts: state.accounts });
      return;
    }
    if (path === "/api/banking/review") {
      await fulfillJson(route, state.queue);
      return;
    }
    if (path === "/api/banking/feed") {
      const accountID = url.searchParams.get("account");
      const transactionState = url.searchParams.get("state");
      await fulfillJson(route, {
        next_cursor: null,
        transactions: state.feed.filter(
          (transaction) =>
            (!accountID || transaction.account_id === Number(accountID)) &&
            (!transactionState || transaction.state === transactionState),
        ),
      });
      return;
    }
    if (path === "/api/banking/recent") {
      await fulfillJson(route, { transactions: state.recent });
      return;
    }
    if (
      path === "/api/banking/accounts/1/import" &&
      request.method() === "POST"
    ) {
      state.imports += 1;
      state.queue = reviewQueueFixture();
      await fulfillJson(route, {
        account_id: 1,
        batch_id: 301,
        duplicates: 1,
        filename: "banking-fixture.csv",
        imported_at: "2026-07-06T10:00:00Z",
        new: 3,
        total: 4,
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
    const candidateMatch = path.match(
      /^\/api\/banking\/transactions\/(\d+)\/invoice-candidates$/,
    );
    if (candidateMatch && request.method() === "GET") {
      await fulfillJson(route, {
        candidates: state.candidates[Number(candidateMatch[1])] ?? [],
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
  candidates: Record<number, BankingInvoiceCandidate[]>;
  feed: BankingTransaction[];
  imports: number;
  queue: BankingReviewQueue;
  recent: BankingRecentTransaction[];
  recodeRequests: Array<{ account_code: string }>;
};

function bankingState(): BankingState {
  return {
    accounts: accountsFixture(),
    candidates: {},
    feed: [],
    imports: 0,
    queue: { matches: [], rules: [], suggestions: [] },
    recent: [],
    recodeRequests: [],
  };
}

function accountsFixture(): BankingAccount[] {
  return [
    {
      created_at: "2026-07-01T09:00:00Z",
      currency: "GBP",
      id: 1,
      ledger_account_code: "1000-cash-gbp",
      name: "Revolut GBP",
      provider: "revolut",
      unreconciled_count: 0,
    },
    {
      created_at: "2026-07-01T09:00:00Z",
      currency: "EUR",
      id: 2,
      ledger_account_code: "1001-cash-eur",
      name: "Revolut EUR",
      provider: "revolut",
      unreconciled_count: 0,
    },
  ];
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

function expenseAccountsFixture() {
  return [
    ledgerAccount({ code: "5010-software", name: "Software" }),
    ledgerAccount({ code: "5020-travel", name: "Travel" }),
  ];
}

function ledgerAccount(overrides: Record<string, unknown>) {
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
