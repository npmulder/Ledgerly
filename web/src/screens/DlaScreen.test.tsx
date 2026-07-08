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

import type { DLABalance, DLAEntry, DLALedger } from "@/api/dla";
import type { LedgerAccount } from "@/api/ledger";
import { DlaScreen } from "@/screens/DlaScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("DlaScreen", () => {
  it("renders the running ledger with mono CR/DR money formatting", async () => {
    vi.stubGlobal("fetch", dlaFetch());

    renderDlaScreen();

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Director's loan · N. Meyer",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("£2,150.00 CR");
    expect(
      screen.getByText("In credit — tax-free to withdraw"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("tablist", { name: "DLA director" })).toBeNull();

    const ledger = screen.getByLabelText("DLA running ledger");
    expect(within(ledger).getAllByText("Drawing")).toHaveLength(2);
    expect(within(ledger).getAllByText("Expense owed")).toHaveLength(2);
    const balanceCell = within(ledger).getByText("£2,150.00 CR").closest("td");
    expect(balanceCell).toHaveClass("ui-table__cell--mono-numeric");
  });

  it("loads the next ledger page with the returned keyset cursor", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = urlFromRequest(input);
      if (url.pathname === "/api/identity/profile") {
        return jsonResponse(identityProfile());
      }
      if (url.pathname === "/api/dla/balance") {
        return jsonResponse(creditBalance());
      }
      if (url.pathname === "/api/dla/ledger") {
        if (url.searchParams.get("cursor") === "page-2") {
          return jsonResponse({
            entries: [
              ledgerEntry({
                date: "2026-07-02",
                description: "Second page repayment",
                id: 5,
                kind: "repayment",
                owed_to_you: { amount_minor: 5000, currency: "GBP" },
                running_balance: { amount_minor: 220000, currency: "GBP" },
              }),
            ],
            next_cursor: null,
          });
        }
        return jsonResponse({
          entries: [creditLedger().entries[0]],
          next_cursor: "page-2",
        });
      }
      return jsonResponse(
        { status: 404, title: "Not Found", type: "about:blank" },
        404,
      );
    });
    vi.stubGlobal("fetch", fetchImpl);

    renderDlaScreen();

    expect(
      await screen.findByText("Company setup costs funded personally"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Load more" }));

    expect(
      await screen.findByText("Second page repayment"),
    ).toBeInTheDocument();
    expect(fetchImpl).toHaveBeenCalledWith(
      "/api/dla/ledger?cursor=page-2&director=director-1",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("switches between director-specific balances and ledger rows", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = urlFromRequest(input);
      const directorID = url.searchParams.get("director") ?? "director-1";
      if (url.pathname === "/api/identity/profile") {
        return jsonResponse(
          identityProfile([
            {
              appointed_date: "2020-07-14",
              id: "director-1",
              is_chair: true,
              name: "N. Meyer",
            },
            {
              appointed_date: "2021-04-01",
              id: "director-2",
              is_chair: false,
              name: "A. Patel",
            },
          ]),
        );
      }
      if (url.pathname === "/api/dla/balance") {
        return jsonResponse(
          directorID === "director-2"
            ? overdrawnBalance(
                {},
                { director_id: "director-2", director_name: "A. Patel" },
              )
            : creditBalance(),
        );
      }
      if (url.pathname === "/api/dla/ledger") {
        return jsonResponse(
          directorID === "director-2"
            ? overdrawnLedger("director-2")
            : creditLedger(),
        );
      }
      if (url.pathname === "/api/ledger/accounts") {
        return jsonResponse({ accounts: expenseAccountsFixture() });
      }
      return jsonResponse(
        { status: 404, title: "Not Found", type: "about:blank" },
        404,
      );
    });
    vi.stubGlobal("fetch", fetchImpl);

    renderDlaScreen();

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Director's loan · N. Meyer",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "N. Meyer" })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    await user.click(screen.getByRole("tab", { name: "A. Patel" }));

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Director's loan · A. Patel",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("£3,000.00 DR");
    expect(await screen.findByText("Director drawing")).toBeInTheDocument();
    expect(fetchImpl).toHaveBeenCalledWith(
      "/api/dla/balance?director=director-2",
      expect.objectContaining({ method: "GET" }),
    );
    expect(fetchImpl).toHaveBeenCalledWith(
      "/api/dla/ledger?director=director-2",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("switches banner and renders overdrawn warning from the policy payload", async () => {
    vi.stubGlobal(
      "fetch",
      dlaFetch({
        balance: overdrawnBalance({
          overdrawn_warning_template:
            "Fixture policy says {{ balance }} is overdrawn from pack payload.",
        }),
        ledger: overdrawnLedger(),
      }),
    );

    renderDlaScreen();

    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveClass(
        "dla-balance-banner--overdrawn",
      );
      expect(screen.getByRole("status")).toHaveTextContent("£3,000.00 DR");
    });
    expect(
      screen.getByText(
        "Fixture policy says £3,000.00 is overdrawn from pack payload.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /clear with dividend/i }),
    ).toBeInTheDocument();
  });

  it("keeps drawing out of the manual-entry form", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", dlaFetch());

    renderDlaScreen();

    const kind = await screen.findByLabelText("Entry kind");
    expect(
      screen.queryByRole("option", { name: "Drawing" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Category")).not.toBeInTheDocument();

    await user.selectOptions(kind, "expense-owed");

    expect(screen.getByLabelText("Category")).toBeInTheDocument();
    expect(
      await screen.findByRole("option", { name: "Software" }),
    ).toBeInTheDocument();
  });

  it("posts a repayment and refreshes the banner optimistically", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(dlaFetchHandler());
    vi.stubGlobal("fetch", fetchImpl);

    renderDlaScreen();

    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent("£2,150.00 CR");
    });
    await user.type(screen.getByLabelText("Amount"), "100.00");
    await user.type(screen.getByLabelText("Description"), "Director repayment");
    await user.click(
      screen.getAllByRole("button", { name: "Record entry" })[1],
    );

    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent("£2,250.00 CR");
    });
    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalledWith(
        "/api/dla/entries",
        expect.objectContaining({ method: "POST" }),
      );
    });
    const postCall = fetchImpl.mock.calls.find(
      ([input, init]) =>
        pathFromRequest(input) === "/api/dla/entries" &&
        init?.method === "POST",
    );
    expect(postCall).toBeDefined();
    expect(JSON.parse(String(postCall?.[1]?.body))).toMatchObject({
      amount: { amount_minor: 10000, currency: "GBP" },
      cash_account_code: "1000-cash-gbp",
      description: "Director repayment",
      director_id: "director-1",
      kind: "repayment",
    });
  });

  it("creates an expense category and posts an expense-owed entry with it", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(dlaFetchHandler());
    vi.stubGlobal("fetch", fetchImpl);

    renderDlaScreen();

    await user.selectOptions(
      await screen.findByLabelText("Entry kind"),
      "expense-owed",
    );
    await user.click(
      await screen.findByRole("button", { name: "New category" }),
    );
    await user.type(screen.getByLabelText("Code"), "5040-training");
    await user.type(screen.getByLabelText("Name"), "Training");
    await user.click(screen.getByRole("button", { name: "Create" }));

    expect(
      await screen.findByRole("option", { name: "Training" }),
    ).toBeInTheDocument();
    await user.type(screen.getByLabelText("Amount"), "42.00");
    await user.type(screen.getByLabelText("Description"), "Training course");
    await user.click(
      screen.getAllByRole("button", { name: "Record entry" })[1],
    );

    await waitFor(() => {
      const postCall = fetchImpl.mock.calls.find(
        ([input, init]) =>
          pathFromRequest(input) === "/api/dla/entries" &&
          init?.method === "POST",
      );
      expect(postCall).toBeDefined();
      expect(JSON.parse(String(postCall?.[1]?.body))).toMatchObject({
        amount: { amount_minor: 4200, currency: "GBP" },
        description: "Training course",
        director_id: "director-1",
        expense_category: "5040-training",
        kind: "expense-owed",
      });
    });
  });

  it("defaults expense-owed entries to Software explicitly", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(dlaFetchHandler());
    vi.stubGlobal("fetch", fetchImpl);

    renderDlaScreen();

    await user.selectOptions(
      await screen.findByLabelText("Entry kind"),
      "expense-owed",
    );
    await screen.findByRole("option", { name: "Software" });
    await user.type(screen.getByLabelText("Amount"), "42.00");
    await user.type(screen.getByLabelText("Description"), "Software renewal");
    await user.click(
      screen.getAllByRole("button", { name: "Record entry" })[1],
    );

    await waitFor(() => {
      const postCall = fetchImpl.mock.calls.find(
        ([input, init]) =>
          pathFromRequest(input) === "/api/dla/entries" &&
          init?.method === "POST",
      );
      expect(postCall).toBeDefined();
      expect(JSON.parse(String(postCall?.[1]?.body))).toMatchObject({
        amount: { amount_minor: 4200, currency: "GBP" },
        description: "Software renewal",
        director_id: "director-1",
        expense_category: "5010-software",
        kind: "expense-owed",
      });
    });
  });
});

function renderDlaScreen() {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });

  render(
    <MemoryRouter>
      <QueryClientProvider client={queryClient}>
        <DlaScreen />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function dlaFetch(overrides: Partial<DlaFixtures> = {}) {
  return vi.fn(dlaFetchHandler(overrides));
}

type DlaFixtures = {
  balance: DLABalance;
  expenseAccounts: LedgerAccount[];
  ledger: DLALedger;
};

function dlaFetchHandler(overrides: Partial<DlaFixtures> = {}) {
  let balance = overrides.balance ?? creditBalance();
  let expenseAccounts = overrides.expenseAccounts ?? expenseAccountsFixture();
  let ledger = overrides.ledger ?? creditLedger();

  return async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = pathFromRequest(input);
    if (path === "/api/identity/profile") {
      return jsonResponse(identityProfile());
    }
    if (path === "/api/dla/balance") {
      return jsonResponse(balance);
    }
    if (path === "/api/dla/ledger") {
      return jsonResponse(ledger);
    }
    if (path === "/api/ledger/accounts" && (init?.method ?? "GET") === "GET") {
      return jsonResponse({ accounts: expenseAccounts });
    }
    if (path === "/api/ledger/accounts" && init?.method === "POST") {
      const body = JSON.parse(String(init.body));
      const account = ledgerAccount({ code: body.code, name: body.name });
      expenseAccounts = [...expenseAccounts, account];
      return jsonResponse(account, 201);
    }
    if (path === "/api/dla/entries" && init?.method === "POST") {
      const entry = JSON.parse(String(init.body));
      balance = {
        ...balance,
        balance: {
          ...balance.balance,
          amount_minor:
            balance.balance.amount_minor + entry.amount.amount_minor,
        },
      };
      ledger = {
        entries: [
          ...ledger.entries,
          ledgerEntry({
            amount: entry.amount,
            balance_side: balance.balance.amount_minor >= 0 ? "CR" : "DR",
            created_at: "2026-07-06T12:00:00Z",
            date: entry.date,
            description: entry.description,
            drawn: { amount_minor: 0, currency: "GBP" },
            id: 99,
            kind: entry.kind,
            owed_to_you: entry.amount,
            running_balance: balance.balance,
            source_ref: "manual:test-repayment",
          }),
        ],
        next_cursor: null,
      };
      return jsonResponse({ source_ref: "manual:test-repayment" }, 201);
    }
    return jsonResponse(
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
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

function creditBalance() {
  return {
    balance: { amount_minor: 215000, currency: "GBP" },
    director_id: "director-1",
    director_name: "N. Meyer",
    policy: policyPayload(),
    status: "credit" as const,
    suggested_clearance: null,
  };
}

function overdrawnBalance(
  policyOverrides: Partial<ReturnType<typeof policyPayload>> = {},
  balanceOverrides: Partial<DLABalance> = {},
) {
  return {
    balance: { amount_minor: -300000, currency: "GBP" },
    director_id: "director-1",
    director_name: "N. Meyer",
    policy: policyPayload(policyOverrides),
    status: "overdrawn" as const,
    suggested_clearance: { amount_minor: 300000, currency: "GBP" },
    ...balanceOverrides,
  };
}

function policyPayload(
  overrides: Partial<{
    bik_warning_key: string;
    credit_explainer_template: string;
    credit_status_text: string;
    overdrawn_warning_template: string;
    remedy: string;
    s455_charge: boolean;
  }> = {},
) {
  return {
    bik_warning_key: "benefit_in_kind_interest_free",
    credit_explainer_template:
      "You can repay yourself up to {{ balance }} at any time with no tax consequence.",
    credit_status_text: "In credit — tax-free to withdraw",
    overdrawn_warning_template:
      "Your loan account is {{ balance }} overdrawn. The Isle of Man has no UK-style s455 charge, but an interest-free loan can create a taxable benefit in kind - charge interest at the official rate or clear it with a dividend.",
    remedy: "clear_with_dividend",
    s455_charge: false,
    ...overrides,
  };
}

function creditLedger() {
  return {
    entries: [
      ledgerEntry({
        date: "2026-05-12",
        description: "Company setup costs funded personally",
        id: 1,
        kind: "expense-owed",
        owed_to_you: { amount_minor: 523800, currency: "GBP" },
        running_balance: { amount_minor: 523800, currency: "GBP" },
      }),
      ledgerEntry({
        balance_side: "CR",
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
        balance_side: "CR",
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

function overdrawnLedger(directorID = "director-1") {
  return {
    entries: [
      ledgerEntry({
        balance_side: "DR",
        date: "2026-07-01",
        description: "Director drawing",
        drawn: { amount_minor: 300000, currency: "GBP" },
        director_id: directorID,
        id: 1,
        kind: "drawing",
        running_balance: { amount_minor: -300000, currency: "GBP" },
      }),
    ],
    next_cursor: null,
  };
}

function ledgerEntry(overrides: Partial<DLAEntry>): DLAEntry {
  return {
    amount: { amount_minor: 0, currency: "GBP" },
    balance_side: "CR",
    created_at: "2026-07-06T12:00:00Z",
    date: "2026-07-06",
    description: "DLA entry",
    director_id: "director-1",
    drawn: { amount_minor: 0, currency: "GBP" },
    id: 1,
    kind: "expense-owed",
    owed_to_you: { amount_minor: 0, currency: "GBP" },
    running_balance: { amount_minor: 0, currency: "GBP" },
    source_ref: "manual:test",
    ...overrides,
  };
}

function identityProfile(
  directors = [
    {
      appointed_date: "2020-07-14",
      id: "director-1",
      is_chair: true,
      name: "N. Meyer",
    },
  ],
) {
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
    shareholders: [
      {
        class: "ordinary £1",
        name: "N. Meyer",
        shares: 100,
      },
    ],
    directors,
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

function pathFromRequest(input: RequestInfo | URL) {
  return urlFromRequest(input).pathname;
}

function urlFromRequest(input: RequestInfo | URL) {
  if (input instanceof Request) {
    return new URL(input.url, "http://localhost");
  }

  return new URL(String(input), "http://localhost");
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
    status,
  });
}
