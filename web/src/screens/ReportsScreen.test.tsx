import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";

import type {
  ReportsFiling,
  ReportsFilingCalendar,
  ReportsPL,
  ReportsVAT,
} from "@/api/reports";
import { ReportsScreen } from "@/screens/ReportsScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("ReportsScreen", () => {
  it("renders P&L fixture grouping, FX gains, CIT, and net total lines", async () => {
    vi.stubGlobal("fetch", reportsFetch());

    renderReportsScreen();

    const pl = await screen.findByLabelText("P&L lines");
    expect(
      within(pl).getByText("Consulting income - Contoso GmbH (EUR)"),
    ).toBeInTheDocument();
    expect(
      within(pl).getByText("Consulting income - Fabrikam Ltd (GBP)"),
    ).toBeInTheDocument();
    expect(
      within(pl).getByText("Realised FX gains on settlement"),
    ).toBeInTheDocument();
    expect(within(pl).getByText("IoM income tax at 0%")).toBeInTheDocument();
    expect(
      within(pl).getByText("Net profit for the period"),
    ).toBeInTheDocument();
    const netAmounts = within(pl).getAllByText("£14,168.25");
    expect(netAmounts[netAmounts.length - 1]).toHaveClass("reports-money");
  });

  it("renders the VAT due badge state from the filing calendar", async () => {
    vi.stubGlobal(
      "fetch",
      reportsFetch({
        calendar: calendarFixture([
          filingFixture({
            due_date: "2026-07-30",
            key: "vat_return",
            label: "VAT return",
            status: "due-soon",
          }),
        ]),
      }),
    );

    renderReportsScreen();

    const [badge] = await screen.findAllByLabelText(
      "VAT return due-soon 30 JUL",
    );
    expect(badge).toHaveClass("reports-due-badge--due-soon");
    expect(badge).toHaveTextContent("DUE 30 JUL");
  });

  it("maps filing calendar statuses to teal, amber, and red badge classes", async () => {
    vi.stubGlobal(
      "fetch",
      reportsFetch({
        calendar: calendarFixture([
          filingFixture({
            due_date: "2026-08-14",
            key: "annual_return",
            label: "Annual return",
            status: "upcoming",
          }),
          filingFixture({
            due_date: "2026-07-30",
            key: "vat_return",
            label: "VAT return",
            status: "due-soon",
          }),
          filingFixture({
            due_date: "2026-04-01",
            key: "company_tax_return",
            label: "Company tax return",
            status: "overdue",
          }),
        ]),
      }),
    );

    renderReportsScreen();

    expect(
      await screen.findByLabelText("Annual return upcoming 14 AUG"),
    ).toHaveClass("reports-due-badge--upcoming");
    const vatBadges = screen.getAllByLabelText("VAT return due-soon 30 JUL");
    expect(vatBadges[vatBadges.length - 1]).toHaveClass(
      "reports-due-badge--due-soon",
    );
    expect(
      screen.getByLabelText("Company tax return overdue 01 APR"),
    ).toHaveClass("reports-due-badge--overdue");
  });

  it("switches quarter presets and refetches the P&L period", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(reportsFetchHandler());
    vi.stubGlobal("fetch", fetchImpl);

    renderReportsScreen();

    await screen.findByText("Profit & loss · Apr-Jun 2026");
    await user.click(screen.getByRole("button", { name: "Jan-Mar" }));

    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalledWith(
        "/api/reports/pl?from=2026-01-01&to=2026-03-31",
        expect.objectContaining({ method: "GET" }),
      );
    });
  });
});

function renderReportsScreen() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <ReportsScreen />
    </QueryClientProvider>,
  );
}

function reportsFetch(overrides: Partial<ReportsFixtures> = {}) {
  return vi.fn(reportsFetchHandler(overrides));
}

type ReportsFixtures = {
  calendar: ReportsFilingCalendar;
  pl: ReportsPL;
  vat: ReportsVAT;
};

function reportsFetchHandler(overrides: Partial<ReportsFixtures> = {}) {
  const fixtures = {
    calendar: calendarFixture(),
    pl: plFixture(),
    vat: vatFixture(),
    ...overrides,
  };

  return async (input: RequestInfo | URL) => {
    const url = urlFromRequest(input);
    if (url.pathname === "/api/reports/pl") {
      return jsonResponse({
        ...fixtures.pl,
        period: {
          from: url.searchParams.get("from") ?? fixtures.pl.period.from,
          to: url.searchParams.get("to") ?? fixtures.pl.period.to,
        },
      });
    }
    if (url.pathname === "/api/reports/vat") {
      return jsonResponse(fixtures.vat);
    }
    if (url.pathname === "/api/reports/calendar") {
      return jsonResponse(fixtures.calendar);
    }
    return jsonResponse(
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
  };
}

function plFixture(): ReportsPL {
  return {
    corporate_tax: {
      amount: money(0),
      label: "IoM income tax at 0%",
      rate: "0.0",
      tax_year: "2026-27",
    },
    expense_total: money(95_645),
    expenses: [
      {
        account_code: "5010-software",
        account_name: "Software & hosting",
        amount: money(21_430),
      },
      {
        account_code: "5020-travel",
        account_name: "Telecoms, travel & admin",
        amount: money(74_215),
      },
    ],
    income: [
      {
        amount: money(1_150_310),
        client_id: "client_contoso",
        client_name: "Contoso GmbH",
        currency: "EUR",
        label: "Consulting income - Contoso GmbH (EUR)",
      },
      {
        amount: money(360_000),
        client_id: "client_fabrikam",
        client_name: "Fabrikam Ltd",
        currency: "GBP",
        label: "Consulting income - Fabrikam Ltd (GBP)",
      },
    ],
    income_total: money(1_512_470),
    net_profit: money(1_416_825),
    period: { from: "2026-04-01", to: "2026-06-30" },
    profit_before_tax: money(1_416_825),
    realised_fx_gains: {
      amount: money(2_160),
      label: "Realised FX gains",
    },
    tax_year: "2026-27",
  };
}

function vatFixture(): ReportsVAT {
  return {
    box1: money(0),
    box4: money(4_120),
    box6: money(1_510_310),
    net_position: money(-4_120),
    period: { from: "2026-04-01", to: "2026-06-30" },
  };
}

function calendarFixture(
  filings: ReportsFiling[] = [
    filingFixture({
      due_date: "2026-07-30",
      key: "vat_return",
      label: "VAT return",
      status: "due-soon",
    }),
    filingFixture({
      due_date: "2026-08-14",
      key: "annual_return",
      label: "Annual return",
      status: "upcoming",
    }),
    filingFixture({
      due_date: "2027-04-01",
      key: "company_tax_return",
      label: "Company tax return",
      status: "upcoming",
    }),
    filingFixture({
      due_date: "2026-10-06",
      key: "personal_tax_return",
      label: "Personal tax return",
      status: "upcoming",
    }),
  ],
): ReportsFilingCalendar {
  return { filings };
}

function filingFixture(overrides: Partial<ReportsFiling>): ReportsFiling {
  return {
    authority: "Isle of Man Customs & Excise",
    days_until: 24,
    due_date: "2026-07-30",
    key: "vat_return",
    label: "VAT return",
    status: "due-soon",
    ...overrides,
  };
}

function money(amount_minor: number) {
  return { amount_minor, currency: "GBP" };
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
