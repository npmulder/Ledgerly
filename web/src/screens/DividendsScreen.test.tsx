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
  DividendsDeclaration,
  DividendsHeadroom,
  DividendsValidationResult,
} from "@/api/dividends";
import { DividendsScreen } from "@/screens/DividendsScreen";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("DividendsScreen", () => {
  it("renders API breakdown lines verbatim with a negative-headroom state", async () => {
    vi.stubGlobal(
      "fetch",
      dividendsFetch({
        headroom: headroomFixture({
          available: { amount: -100000, currency: "GBP" },
          distributable: false,
          lines: [
            moneyLine("Retained earnings b/fwd", 0),
            moneyLine("Profit YTD (after expenses)", -100000),
            moneyLine("Corporation tax provision at 0%", 0),
            moneyLine("Dividends already declared YTD", 0),
            moneyLine("Available to distribute", -100000),
          ],
        }),
      }),
    );

    renderDividendsScreen();

    const breakdown = await screen.findByLabelText(
      "Dividend headroom breakdown",
    );
    expect(within(breakdown).getByText("Retained earnings b/fwd"))
      .toBeInTheDocument();
    expect(within(breakdown).getByText("Profit YTD (after expenses)"))
      .toBeInTheDocument();
    expect(within(breakdown).getByText("Corporation tax provision at 0%"))
      .toBeInTheDocument();
    expect(within(breakdown).getByText("Dividends already declared YTD"))
      .toBeInTheDocument();
    expect(within(breakdown).getByText("Available to distribute"))
      .toBeInTheDocument();
    expect(within(breakdown).getAllByText("-£1,000.00").length)
      .toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Not currently distributable")).toBeInTheDocument();
  });

  it("renders validation strip ok and blocked states", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", dividendsFetch());

    renderDividendsScreen();

    await user.type(await screen.findByLabelText("Amount"), "1000.00");

    expect(await screen.findByText("Within headroom ✓")).toBeInTheDocument();
    expect(screen.getByText("No WHT ✓")).toBeInTheDocument();
    expect(
      screen.getByText("set aside personally £100.00"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Generate voucher + minutes" }),
    ).toBeEnabled();

    await user.clear(screen.getByLabelText("Amount"));
    await user.type(screen.getByLabelText("Amount"), "20000.00");

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Over headroom",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Distributable figure £17,160.00",
    );
    expect(
      screen.getByRole("button", { name: "Generate voucher + minutes" }),
    ).toBeDisabled();
  });

  it("honors ?amount= prefill and validates on load", async () => {
    const fetchImpl = dividendsFetch();
    vi.stubGlobal("fetch", fetchImpl);

    renderDividendsScreen("/dividends?amount=3000.00");

    expect(await screen.findByLabelText("Amount")).toHaveValue("3000.00");
    expect(await screen.findByText("Within headroom ✓")).toBeInTheDocument();
    await waitFor(() => {
      const validationCall = fetchImpl.mock.calls.find(
        ([input, init]) =>
          pathFromRequest(input) === "/api/dividends/validate" &&
          init?.method === "POST",
      );
      expect(validationCall).toBeDefined();
      expect(JSON.parse(String(validationCall?.[1]?.body))).toMatchObject({
        amount: { amount: 300000, currency: "GBP" },
      });
    });
  });
});

function renderDividendsScreen(initialEntry = "/dividends") {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <QueryClientProvider client={queryClient}>
        <DividendsScreen />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

type DividendsFixtures = {
  declarations: DividendsDeclaration[];
  headroom: DividendsHeadroom;
};

function dividendsFetch(overrides: Partial<DividendsFixtures> = {}) {
  const headroom = overrides.headroom ?? headroomFixture();
  const declarations = overrides.declarations ?? [];
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = pathFromRequest(input);
    const method = init?.method ?? "GET";
    if (path === "/api/dividends/headroom") {
      return jsonResponse(headroom);
    }
    if (path === "/api/dividends/history") {
      return jsonResponse({ declarations });
    }
    if (path === "/api/dividends/validate" && method === "POST") {
      const body = JSON.parse(String(init?.body));
      return jsonResponse(validationFixture(body.amount.amount, headroom));
    }
    return jsonResponse(
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
      "application/problem+json",
    );
  });
}

function headroomFixture(
  overrides: Partial<DividendsHeadroom> = {},
): DividendsHeadroom {
  return {
    as_of: "2026-07-06T00:00:00Z",
    available: { amount: 1716000, currency: "GBP" },
    distributable: true,
    financial_year: "2026-27",
    lines: [
      moneyLine("Retained earnings b/fwd", 1200000),
      moneyLine("Profit YTD (after expenses)", 516000),
      moneyLine("Corporation tax provision at 0%", 0),
      moneyLine("Dividends already declared YTD", 0),
      moneyLine("Available to distribute", 1716000),
    ],
    ...overrides,
  };
}

function validationFixture(
  amount: number,
  headroom: DividendsHeadroom,
): DividendsValidationResult {
  const within = headroom.distributable && amount <= headroom.available.amount;
  return {
    amount: { amount, currency: "GBP" },
    distributable: headroom.distributable,
    distributable_total: headroom.available,
    headroom,
    personal_tax: {
      marginal: { amount: Math.round(amount * 0.1), currency: "GBP" },
      message: `set aside personally ${formatGBP(Math.round(amount * 0.1))}`,
      prior_ytd: { amount: 0, currency: "GBP" },
      tax_year: "2026-27",
      with_dividend: { amount, currency: "GBP" },
    },
    withholding: {
      applies: false,
      informational: true,
      policy: "none",
      tax_year: "2026-27",
    },
    within_headroom: within,
  };
}

function moneyLine(label: string, amount: number) {
  return {
    amount: { amount, currency: "GBP" },
    label,
  };
}

function pathFromRequest(input: RequestInfo | URL) {
  return new URL(String(input), "http://ledgerly.test").pathname;
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

function formatGBP(amount: number) {
  return new Intl.NumberFormat("en-GB", {
    currency: "GBP",
    style: "currency",
  }).format(amount / 100);
}
