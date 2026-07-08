import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type {
  BankingAccount,
  BankingCommandResponse,
  BankingInvoiceCandidate,
  BankingMoney,
  BankingRecentTransaction,
  BankingReceipt,
  BankingReviewCard,
  BankingReviewQueue,
  BankingTransaction,
} from "@/api/banking";
import type { LedgerAccount } from "@/api/ledger";
import { BankingScreen } from "@/screens/BankingScreen";
import { formatConfidence } from "@/screens/bankingFormat";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("BankingScreen", () => {
  it("renders account badges and three review card variants from fixtures", async () => {
    vi.stubGlobal(
      "fetch",
      bankingApi({
        accounts: accountsFixture(),
        queue: reviewQueueFixture(),
        recent: recentFixture(),
      }).fetch,
    );

    renderBanking();

    expect(screen.getByRole("link", { name: "Payee rules" })).toHaveAttribute(
      "href",
      "/banking/payee-rules",
    );

    const accountList = await screen.findByLabelText("Bank accounts");
    const gbpCard = within(accountList)
      .getByText("Revolut GBP")
      .closest("button");
    expect(gbpCard).not.toBeNull();
    expect(gbpCard).toHaveAttribute("aria-pressed", "true");
    expect(within(gbpCard as HTMLElement).getByText("2")).toBeInTheDocument();

    const eurCard = within(accountList)
      .getByText("Revolut EUR")
      .closest("button");
    expect(eurCard).not.toBeNull();
    expect(eurCard).toHaveAttribute("aria-pressed", "false");
    expect(within(eurCard as HTMLElement).getByText("1")).toBeInTheDocument();

    expect(screen.getByText("Invoice match")).toBeInTheDocument();
    expect(screen.getByText("98% match")).toBeInTheDocument();
    expect(screen.getAllByText("INV-2026-07").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Contoso GmbH/).length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "Confirm" })).toBeEnabled();

    expect(screen.getByText("DLA suggestion")).toBeInTheDocument();
    expect(screen.getByText("TRANSFER TO N MEYER")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "File to DLA" })).toBeEnabled();

    await userEvent.click(eurCard as HTMLElement);
    expect(await screen.findByText("Payee rule")).toBeInTheDocument();
    expect(screen.getAllByText("HETZNER ONLINE").length).toBeGreaterThan(0);
    expect(screen.getByText(/Applied 11 times/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply" })).toBeEnabled();
  });

  it("formats confidence values as whole-percent match labels", () => {
    expect(formatConfidence(0.982)).toBe("98% match");
    expect(formatConfidence(91)).toBe("91% match");
  });

  it("creates a GBP account from the empty state and imports a CSV", async () => {
    const user = userEvent.setup();
    const api = bankingApi({
      accounts: [],
      queue: { matches: [], rules: [], suggestions: [] },
      recent: [],
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    expect(await screen.findByText("No bank accounts")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import CSV" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Add account" }));
    const dialog = await screen.findByRole("dialog", {
      name: "Add bank account",
    });
    await user.type(
      within(dialog).getByLabelText("Account name"),
      "Operating GBP",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create account" }),
    );

    expect(
      await screen.findByText("Created Operating GBP. CSV import ready."),
    ).toBeInTheDocument();
    const accountList = await screen.findByLabelText("Bank accounts");
    const accountCard = within(accountList)
      .getByText("Operating GBP")
      .closest("button");
    expect(accountCard).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Import CSV" })).toBeEnabled();

    await user.upload(
      screen.getByLabelText("CSV statement file"),
      new File(["Date,Description,Money In,Money Out\n"], "statement.csv", {
        type: "text/csv",
      }),
    );

    await waitFor(() => {
      expect(
        api.fetch.mock.calls.some(
          ([input, init]) =>
            urlFromRequest(input).pathname ===
              "/api/banking/accounts/1/import" && init?.method === "POST",
        ),
      ).toBe(true);
    });
    expect(
      await screen.findByText("statement.csv: 0 new, 0 duplicates"),
    ).toBeInTheDocument();
  });

  it("creates a EUR account from the populated account list and selects it", async () => {
    const user = userEvent.setup();
    const api = bankingApi({
      accounts: accountsFixture(),
      queue: { matches: [], rules: [], suggestions: [] },
      recent: [],
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const accountList = await screen.findByLabelText("Bank accounts");
    await user.click(
      within(accountList).getByRole("button", { name: "Add account" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Add bank account",
    });
    await user.type(
      within(dialog).getByLabelText("Account name"),
      "Operating EUR",
    );
    await user.selectOptions(within(dialog).getByLabelText("Currency"), "EUR");
    await user.click(
      within(dialog).getByRole("button", { name: "Create account" }),
    );

    await waitFor(() => {
      const createCall = api.fetch.mock.calls.find(
        ([input, init]) =>
          urlFromRequest(input).pathname === "/api/banking/accounts" &&
          init?.method === "POST",
      );
      expect(JSON.parse(String(createCall?.[1]?.body))).toMatchObject({
        currency: "EUR",
        name: "Operating EUR",
        provider: "revolut",
      });
    });
    const newCard = within(await screen.findByLabelText("Bank accounts"))
      .getByText("Operating EUR")
      .closest("button");
    expect(newCard).toHaveAttribute("aria-pressed", "true");
  });

  it("shows validation and duplicate account errors without posting", async () => {
    const user = userEvent.setup();
    const api = bankingApi({
      accounts: accountsFixture(),
      queue: { matches: [], rules: [], suggestions: [] },
      recent: [],
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const accountList = await screen.findByLabelText("Bank accounts");
    await user.click(
      within(accountList).getByRole("button", { name: "Add account" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Add bank account",
    });

    await user.click(
      within(dialog).getByRole("button", { name: "Create account" }),
    );
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Enter an account name.",
    );

    await user.type(
      within(dialog).getByLabelText("Account name"),
      "Revolut GBP",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create account" }),
    );
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      'A Revolut Business GBP account named "Revolut GBP" already exists.',
    );
    expect(
      api.fetch.mock.calls.some(
        ([input, init]) =>
          urlFromRequest(input).pathname === "/api/banking/accounts" &&
          init?.method === "POST",
      ),
    ).toBe(false);
  });

  it("shows caught-up empty state when the selected account queue is empty", async () => {
    vi.stubGlobal(
      "fetch",
      bankingApi({
        accounts: accountsFixture(),
        queue: { matches: [], rules: [], suggestions: [] },
        recent: [],
      }).fetch,
    );

    renderBanking();

    expect(await screen.findAllByText("All caught up…")).toHaveLength(2);
    expect(
      await screen.findByText("No reconciliations yet."),
    ).toBeInTheDocument();
  });

  it("does not show caught-up state for unsuggested imports", async () => {
    const accounts = accountsFixture().map((account) =>
      account.id === 1 ? { ...account, unreconciled_count: 1 } : account,
    );
    vi.stubGlobal(
      "fetch",
      bankingApi({
        accounts,
        queue: { matches: [], rules: [], suggestions: [] },
        recent: [],
      }).fetch,
    );

    renderBanking();

    expect(await screen.findByText("Review pending")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Imported transactions are waiting for suggested matches.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("All caught up…")).not.toBeInTheDocument();
  });

  it("matches an unreconciled inbound transaction to a selected invoice", async () => {
    const user = userEvent.setup();
    const accounts = accountsFixture().map((account) =>
      account.id === 1 ? { ...account, unreconciled_count: 1 } : account,
    );
    const api = bankingApi({
      accounts,
      candidates: {
        301: [
          invoiceCandidate({
            client: "Manual Client",
            invoice_id: "inv_manual",
            invoice_number: "INV-MANUAL",
          }),
        ],
      },
      feed: [
        transactionFixture({
          id: 301,
          payee: "MANUAL CLIENT",
          reference: "bank transfer",
          state: "unreconciled",
        }),
      ],
      queue: { matches: [], rules: [], suggestions: [] },
      recent: [],
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const card = (await screen.findByText("Manual match")).closest("article");
    expect(card).not.toBeNull();
    await user.click(
      within(card as HTMLElement).getByText("Match to invoice ▾"),
    );
    await user.selectOptions(
      within(card as HTMLElement).getByLabelText("Invoice"),
      "inv_manual",
    );
    await user.click(
      within(card as HTMLElement).getByRole("button", {
        name: "Match selected",
      }),
    );

    await waitFor(() => {
      const confirmCall = api.fetch.mock.calls.find(
        ([input, init]) =>
          urlFromRequest(input).pathname ===
            "/api/banking/transactions/301/confirm" && init?.method === "POST",
      );
      expect(confirmCall).toBeDefined();
      expect(JSON.parse(String(confirmCall?.[1]?.body))).toMatchObject({
        invoice_id: "inv_manual",
      });
    });
    expect(await screen.findByText("Confirmed match")).toBeInTheDocument();
  });

  it("confirms an automatic match against a different selected invoice", async () => {
    const user = userEvent.setup();
    const api = bankingApi({
      accounts: accountsFixture(),
      candidates: {
        101: [
          invoiceCandidate({
            client: "Contoso GmbH",
            invoice_id: "inv_2026_07",
            invoice_number: "INV-2026-07",
          }),
          invoiceCandidate({
            client: "Fabrikam Ltd",
            invoice_id: "inv_override",
            invoice_number: "INV-OVERRIDE",
          }),
        ],
      },
      queue: reviewQueueFixture(),
      recent: recentFixture(),
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const card = (await screen.findByText("Invoice match")).closest("article");
    expect(card).not.toBeNull();
    await user.click(
      within(card as HTMLElement).getByText("Match to invoice ▾"),
    );
    await user.selectOptions(
      within(card as HTMLElement).getByLabelText("Invoice"),
      "inv_override",
    );
    await user.click(
      within(card as HTMLElement).getByRole("button", {
        name: "Confirm selected",
      }),
    );

    await waitFor(() => {
      const confirmCall = api.fetch.mock.calls.find(
        ([input, init]) =>
          urlFromRequest(input).pathname ===
            "/api/banking/transactions/101/confirm" && init?.method === "POST",
      );
      expect(confirmCall).toBeDefined();
      expect(JSON.parse(String(confirmCall?.[1]?.body))).toMatchObject({
        invoice_id: "inv_override",
      });
    });
  });

  it("matches an inbound payee-rule suggestion to a selected invoice", async () => {
    const user = userEvent.setup();
    const api = bankingApi({
      accounts: accountsFixture(),
      candidates: {
        104: [
          invoiceCandidate({
            amount: money(450000, "GBP"),
            client: "Rule Client",
            invoice_id: "inv_rule_manual",
            invoice_number: "INV-RULE",
          }),
        ],
      },
      queue: {
        matches: [],
        rules: [
          reviewCard({
            confidence: 0.91,
            explanation: "Recurring payee matched the software rule.",
            kind: "rule",
            suggestion_id: 9004,
            target: {
              account_code: "5010-software",
              times_applied: 11,
              type: "account",
            },
            transaction: transactionFixture({
              amount: money(450000, "GBP"),
              id: 104,
              payee: "CLIENT RULE PAYMENT",
              reference: "invoice paid",
            }),
          }),
        ],
        suggestions: [],
      },
      recent: [],
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const card = (await screen.findByText("Payee rule")).closest("article");
    expect(card).not.toBeNull();
    expect(
      within(card as HTMLElement).getByRole("button", { name: "Apply" }),
    ).toBeInTheDocument();
    expect(
      within(card as HTMLElement).getByText("Recode ▾"),
    ).toBeInTheDocument();
    await user.click(
      within(card as HTMLElement).getByText("Match to invoice ▾"),
    );
    await user.selectOptions(
      within(card as HTMLElement).getByLabelText("Invoice"),
      "inv_rule_manual",
    );
    await user.click(
      within(card as HTMLElement).getByRole("button", {
        name: "Match selected",
      }),
    );

    await waitFor(() => {
      const confirmCall = api.fetch.mock.calls.find(
        ([input, init]) =>
          urlFromRequest(input).pathname ===
            "/api/banking/transactions/104/confirm" && init?.method === "POST",
      );
      expect(confirmCall).toBeDefined();
      expect(JSON.parse(String(confirmCall?.[1]?.body))).toMatchObject({
        invoice_id: "inv_rule_manual",
      });
    });
  });

  it("requests recently reconciled rows for the selected account", async () => {
    const api = bankingApi({
      accounts: accountsFixture(),
      queue: reviewQueueFixture(),
      recent: [
        ...recentFixture(),
        {
          actor: "reconciliation-command",
          reconciled_at: "2026-07-06T11:00:00Z",
          transaction: transactionFixture({
            account_id: 2,
            id: 202,
            payee: "EUR Client",
            reference: "EUR invoice",
          }),
        },
      ],
    });
    const user = userEvent.setup();
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const accountList = await screen.findByLabelText("Bank accounts");
    await waitFor(() => {
      expect(recentRequestAccounts(api.fetch)).toContain("1");
    });

    await user.click(within(accountList).getByText("Revolut EUR"));

    await waitFor(() => {
      expect(recentRequestAccounts(api.fetch)).toContain("2");
    });
    expect(await screen.findByText("EUR Client")).toBeInTheDocument();
    expect(screen.queryByText("Fabrikam Ltd")).not.toBeInTheDocument();
  });

  it("hides zero-value realised FX notices when confirming matches", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      bankingApi({
        accounts: accountsFixture(),
        confirmResponse: {
          kind: "match",
          realised_fx_amount: money(0, "GBP"),
          transaction: transactionFixture({ state: "reconciled" }),
        },
        queue: reviewQueueFixture(),
        recent: recentFixture(),
      }).fetch,
    );

    renderBanking();

    await user.click(await screen.findByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("Confirmed match")).toBeInTheDocument();
    expect(screen.queryByText(/auto-posted FX/)).not.toBeInTheDocument();
  });

  it("rolls back optimistic exclude when the API returns a 409", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "prompt",
      vi.fn(() => "duplicate import"),
    );
    vi.stubGlobal(
      "fetch",
      bankingApi({
        accounts: accountsFixture(),
        excludeStatus: 409,
        queue: reviewQueueFixture(),
        recent: [],
      }).fetch,
    );

    renderBanking();

    const card = (await screen.findByText("Invoice match")).closest("article");
    expect(card).not.toBeNull();
    await user.click(
      within(card as HTMLElement).getByLabelText(
        "Options for CONTOSO GMBH SEPA",
      ),
    );
    await user.click(within(card as HTMLElement).getByRole("menuitem"));

    expect(
      await screen.findByText("Exclude conflict; restored the card."),
    ).toBeInTheDocument();
    expect(screen.getByText("Invoice match")).toBeInTheDocument();
  });

  it("attaches, previews, and deletes receipts on review cards", async () => {
    const user = userEvent.setup();
    const queue = reviewQueueFixture();
    const receipt = receiptFixture();
    const fetchMock = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = urlFromRequest(input);
        const method = init?.method ?? "GET";

        if (url.pathname === "/api/banking/accounts") {
          return jsonResponse({ accounts: accountsFixture() });
        }
        if (url.pathname === "/api/banking/review") {
          return jsonResponse(queue);
        }
        if (url.pathname === "/api/banking/recent") {
          return jsonResponse({ transactions: [] });
        }
        if (
          url.pathname === "/api/banking/transactions/101/receipt" &&
          method === "PUT"
        ) {
          queue.matches[0].transaction.receipt = receipt;
          return jsonResponse(receipt);
        }
        if (
          url.pathname === "/api/banking/transactions/101/receipt" &&
          method === "DELETE"
        ) {
          queue.matches[0].transaction.receipt = null;
          return new Response(null, { status: 204 });
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchMock);

    renderBanking();

    const input = await screen.findByLabelText(
      "Receipt file for CONTOSO GMBH SEPA",
    );
    await user.upload(
      input,
      new File(["%PDF-1.4\n% receipt\n%%EOF\n"], "receipt.pdf", {
        type: "application/pdf",
      }),
    );

    expect(await screen.findByText("Attached receipt.pdf")).toBeInTheDocument();
    const preview = await screen.findByRole("link", {
      name: "Preview receipt",
    });
    expect(preview).toHaveAttribute(
      "href",
      "/api/banking/transactions/101/receipt",
    );
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/banking/transactions/101/receipt",
      expect.objectContaining({ method: "PUT" }),
    );

    await user.click(screen.getByRole("button", { name: "Delete receipt" }));

    expect(await screen.findByText("Receipt removed.")).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "Preview receipt" }),
    ).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/banking/transactions/101/receipt",
      expect.objectContaining({ method: "DELETE" }),
    );
  });

  it("creates an expense category from the recode picker and uses it", async () => {
    const user = userEvent.setup();
    const api = bankingApi({
      accounts: accountsFixture(),
      queue: reviewQueueFixture(),
      recent: recentFixture(),
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const card = (await screen.findByText("DLA suggestion")).closest("article");
    expect(card).not.toBeNull();
    await user.click(within(card as HTMLElement).getByText("Recode ▾"));
    await user.click(
      await within(card as HTMLElement).findByRole("button", {
        name: "New category",
      }),
    );
    await user.type(
      within(card as HTMLElement).getByLabelText("Code"),
      "5040-training",
    );
    await user.type(
      within(card as HTMLElement).getByLabelText("Name"),
      "Training",
    );
    await user.click(
      within(card as HTMLElement).getByRole("button", { name: "Create" }),
    );

    expect(
      await within(card as HTMLElement).findByRole("option", {
        name: "Training",
      }),
    ).toBeInTheDocument();
    await user.click(
      within(card as HTMLElement).getByRole("button", {
        name: "Recode selected",
      }),
    );

    await waitFor(() => {
      const recodeCall = api.fetch.mock.calls.find(
        ([input, init]) =>
          urlFromRequest(input).pathname ===
            "/api/banking/transactions/102/recode" && init?.method === "POST",
      );
      expect(recodeCall).toBeDefined();
      expect(JSON.parse(String(recodeCall?.[1]?.body))).toMatchObject({
        account_code: "5040-training",
      });
    });
  });

  it("does not create a category when Enter activates Cancel", async () => {
    const user = userEvent.setup();
    const api = bankingApi({
      accounts: accountsFixture(),
      queue: reviewQueueFixture(),
      recent: recentFixture(),
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const card = (await screen.findByText("DLA suggestion")).closest("article");
    expect(card).not.toBeNull();
    await user.click(within(card as HTMLElement).getByText("Recode ▾"));
    await user.click(
      await within(card as HTMLElement).findByRole("button", {
        name: "New category",
      }),
    );
    await user.type(
      within(card as HTMLElement).getByLabelText("Code"),
      "5040-training",
    );
    await user.type(
      within(card as HTMLElement).getByLabelText("Name"),
      "Training",
    );

    const cancel = within(card as HTMLElement).getByRole("button", {
      name: "Cancel",
    });
    cancel.focus();
    await user.keyboard("{Enter}");

    expect(
      within(card as HTMLElement).queryByLabelText("Code"),
    ).not.toBeInTheDocument();
    expect(
      api.fetch.mock.calls.some(
        ([input, init]) =>
          urlFromRequest(input).pathname === "/api/ledger/accounts" &&
          init?.method === "POST",
      ),
    ).toBe(false);
  });

  it("defaults suggestion recodes to Software explicitly", async () => {
    const user = userEvent.setup();
    const api = bankingApi({
      accounts: accountsFixture(),
      queue: reviewQueueFixture(),
      recent: recentFixture(),
    });
    vi.stubGlobal("fetch", api.fetch);

    renderBanking();

    const card = (await screen.findByText("DLA suggestion")).closest("article");
    expect(card).not.toBeNull();
    await user.click(within(card as HTMLElement).getByText("Recode ▾"));
    await within(card as HTMLElement).findByRole("option", {
      name: "Software",
    });
    await user.click(
      within(card as HTMLElement).getByRole("button", {
        name: "Recode selected",
      }),
    );

    await waitFor(() => {
      const recodeCall = api.fetch.mock.calls.find(
        ([input, init]) =>
          urlFromRequest(input).pathname ===
            "/api/banking/transactions/102/recode" && init?.method === "POST",
      );
      expect(recodeCall).toBeDefined();
      expect(JSON.parse(String(recodeCall?.[1]?.body))).toMatchObject({
        account_code: "5010-software",
      });
    });
  });
});

function renderBanking() {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <BankingScreen />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function bankingApi({
  accounts,
  candidates = {},
  confirmResponse,
  excludeStatus = 200,
  feed = [],
  queue,
  recent,
}: {
  accounts: BankingAccount[];
  candidates?: Record<number, BankingInvoiceCandidate[]>;
  confirmResponse?: BankingCommandResponse;
  excludeStatus?: number;
  feed?: BankingTransaction[];
  queue: BankingReviewQueue;
  recent: BankingRecentTransaction[];
}) {
  let expenseAccounts = expenseAccountsFixture();
  let bankingAccounts = [...accounts];
  let nextAccountID =
    Math.max(0, ...bankingAccounts.map((account) => account.id)) + 1;
  return {
    fetch: vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = urlFromRequest(input);
      const path = url.pathname;
      const method = init?.method ?? "GET";

      if (path === "/api/ledger/accounts" && method === "GET") {
        return jsonResponse({ accounts: expenseAccounts });
      }
      if (path === "/api/ledger/accounts" && method === "POST") {
        const body = JSON.parse(String(init?.body));
        const account = ledgerAccount({
          code: body.code,
          name: body.name,
        });
        expenseAccounts = [...expenseAccounts, account];
        return jsonResponse(account, 201);
      }
      if (path === "/api/banking/accounts" && method === "GET") {
        return jsonResponse({ accounts: bankingAccounts });
      }
      if (path === "/api/banking/accounts" && method === "POST") {
        const body = JSON.parse(String(init?.body));
        const account = bankingAccount({
          currency: body.currency,
          id: nextAccountID,
          ledger_account_code: `1000-cash-revolut-${body.currency.toLowerCase()}`,
          name: body.name,
          provider: body.provider,
        });
        nextAccountID += 1;
        bankingAccounts = [...bankingAccounts, account];
        return jsonResponse(account, 201);
      }
      if (path.match(/^\/api\/banking\/accounts\/\d+\/import$/)) {
        return jsonResponse({
          account_id: Number(path.split("/")[4]),
          batch_id: 99,
          duplicates: 0,
          filename: "statement.csv",
          imported_at: "2026-07-07T10:00:00Z",
          new: 0,
          total: 0,
        });
      }
      if (path === "/api/banking/review") {
        return jsonResponse(queue);
      }
      if (path === "/api/banking/feed") {
        const accountID = url.searchParams.get("account");
        const state = url.searchParams.get("state");
        return jsonResponse({
          next_cursor: null,
          transactions: feed.filter(
            (transaction) =>
              (!accountID || transaction.account_id === Number(accountID)) &&
              (!state || transaction.state === state),
          ),
        });
      }
      if (path === "/api/banking/recent") {
        const accountID = url.searchParams.get("account");
        return jsonResponse({
          transactions: accountID
            ? recent.filter(
                (item) => item.transaction.account_id === Number(accountID),
              )
            : recent,
        });
      }
      const candidateMatch = path.match(
        /^\/api\/banking\/transactions\/(\d+)\/invoice-candidates$/,
      );
      if (candidateMatch && method === "GET") {
        return jsonResponse({
          candidates: candidates[Number(candidateMatch[1])] ?? [],
        });
      }
      if (
        path === "/api/banking/transactions/101/confirm" &&
        method === "POST"
      ) {
        return jsonResponse(
          confirmResponse ?? {
            kind: "match",
            realised_fx_amount: money(321, "GBP"),
            transaction: transactionFixture({ state: "reconciled" }),
          },
        );
      }
      if (path.endsWith("/confirm") && method === "POST") {
        const parts = path.split("/");
        const transactionID = Number(parts[parts.length - 2]);
        return jsonResponse({
          kind: "match",
          realised_fx_amount: money(0, "GBP"),
          transaction: transactionFixture({
            id: transactionID,
            state: "reconciled",
          }),
        });
      }
      if (
        path === "/api/banking/transactions/101/exclude" &&
        method === "POST"
      ) {
        if (excludeStatus === 409) {
          return jsonResponse(
            {
              detail: "banking: transaction 101 is already reconciled",
              status: 409,
              title: "Conflict",
              type: "about:blank",
            },
            409,
            "application/problem+json",
          );
        }
        return jsonResponse({ state_change: { transaction_id: 101 } });
      }
      if (path.endsWith("/recode") && method === "POST") {
        const parts = path.split("/");
        const transactionID = Number(parts[parts.length - 2]);
        const body = JSON.parse(String(init?.body));
        return jsonResponse({
          kind: "rule",
          rule: {
            account_code: body.account_code,
            created_at: "2026-07-06T10:00:00Z",
            created_from: "recode",
            id: 77,
            last_applied_at: "2026-07-06T10:00:00Z",
            match_mode: "exact",
            matcher: "TRANSFER TO N MEYER",
            times_applied: 1,
          },
          transaction: transactionFixture({
            id: transactionID,
            state: "reconciled",
          }),
        });
      }
      return jsonResponse(
        { status: 404, title: "Not Found", type: "about:blank" },
        404,
        "application/problem+json",
      );
    }),
  };
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
        confidence: 0.982,
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

function recentFixture(): BankingRecentTransaction[] {
  return [
    {
      actor: "reconciliation-command",
      reconciled_at: "2026-07-06T10:00:00Z",
      transaction: transactionFixture({
        id: 201,
        payee: "Fabrikam Ltd",
        reference: "INV-2026-06",
      }),
    },
  ];
}

function invoiceCandidate(
  overrides: Partial<BankingInvoiceCandidate> = {},
): BankingInvoiceCandidate {
  return {
    amount: money(450000, "EUR"),
    client: "Contoso GmbH",
    due_date: "2026-08-05",
    invoice_id: "inv_2026_07",
    invoice_number: "INV-2026-07",
    issue_date: "2026-07-06",
    status: "sent",
    ...overrides,
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

function receiptFixture(): BankingReceipt {
  return {
    content_type: "application/pdf",
    filename: "receipt.pdf",
    size: 31,
    uploaded_at: "2026-07-06T09:15:00Z",
    url: "/api/banking/transactions/101/receipt",
  };
}

function money(amountMinor: number, currency: string): BankingMoney {
  return { amount_minor: amountMinor, currency };
}

function recentRequestAccounts(fetchMock: ReturnType<typeof vi.fn>) {
  return fetchMock.mock.calls
    .map(([input]) => urlFromRequest(input).searchParams.get("account"))
    .filter((accountID): accountID is string => accountID !== null);
}

function urlFromRequest(input: RequestInfo | URL) {
  const url =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.toString()
        : input.url;
  return new URL(url, "http://ledgerly.test");
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
