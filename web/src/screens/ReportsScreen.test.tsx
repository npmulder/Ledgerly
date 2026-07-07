import {
  cleanup,
  fireEvent,
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
  vi.restoreAllMocks();
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

  it("preserves minus signs for negative report totals", async () => {
    vi.stubGlobal(
      "fetch",
      reportsFetch({
        pl: {
          ...plFixture(),
          net_profit: money(-1_234),
          profit_before_tax: money(-1_234),
          realised_fx_gains: {
            amount: money(-2_160),
            label: "Realised FX gains",
          },
        },
      }),
    );

    renderReportsScreen();

    const pl = await screen.findByLabelText("P&L lines");
    expect(within(pl).getByText("-£21.60")).toBeInTheDocument();
    expect(within(pl).getAllByText("-£12.34")).toHaveLength(2);
    expect(within(pl).getByText("(£214.30)")).toBeInTheDocument();
  });

  it("renders the VAT due badge state from the filing calendar", async () => {
    const reportYear = currentReportYear();
    vi.stubGlobal(
      "fetch",
      reportsFetch({
        calendar: calendarFixture([
          filingFixture({
            due_date: `${reportYear}-07-30`,
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

  it("omits the VAT filing row and due badge when the calendar has no VAT filing", async () => {
    vi.stubGlobal(
      "fetch",
      reportsFetch({
        calendar: calendarFixture(
          defaultCalendarFilings().filter(
            (filing) => filing.key !== "vat_return",
          ),
        ),
      }),
    );

    renderReportsScreen();

    const calendar = await screen.findByRole("list");
    expect(within(calendar).queryByText("VAT return")).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText(/VAT return due-soon/),
    ).not.toBeInTheDocument();
  });

  it("shows a neutral VAT note without return figures when not registered", async () => {
    vi.stubGlobal(
      "fetch",
      reportsFetch({
        calendar: calendarFixture([]),
        vat: notRegisteredVATFixture(),
      }),
    );

    renderReportsScreen();

    expect(await screen.findByText("Not VAT registered.")).toBeInTheDocument();
    expect(
      screen.queryByText("Box 1 · VAT due on sales"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Box 4 · VAT reclaimed")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Box 6 · Total sales ex-VAT"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("Net payable to IoM C&E"),
    ).not.toBeInTheDocument();
  });

  it("maps filing calendar statuses to teal, amber, and red badge classes", async () => {
    const reportYear = currentReportYear();
    vi.stubGlobal(
      "fetch",
      reportsFetch({
        calendar: calendarFixture([
          filingFixture({
            due_date: `${reportYear}-08-14`,
            key: "annual_return",
            label: "Annual return",
            status: "upcoming",
          }),
          filingFixture({
            due_date: `${reportYear}-07-30`,
            key: "vat_return",
            label: "VAT return",
            status: "due-soon",
          }),
          filingFixture({
            due_date: `${reportYear}-04-01`,
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
    const reportYear = currentReportYear();

    renderReportsScreen();

    await screen.findByText(`Profit & loss · Apr-Jun ${reportYear}`);
    await user.click(screen.getByRole("button", { name: "Jan-Mar" }));

    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalledWith(
        `/api/reports/pl?from=${reportYear}-01-01&to=${reportYear}-03-31`,
        expect.objectContaining({ method: "GET" }),
      );
    });
  });

  it("starts an export download for the selected period", async () => {
    const user = userEvent.setup();
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);
    vi.stubGlobal("fetch", reportsFetch());
    const reportYear = currentReportYear();

    renderReportsScreen();

    await screen.findByText(`Profit & loss · Apr-Jun ${reportYear}`);
    const exportButton = screen.getByRole("button", { name: "Export pack" });
    expect(exportButton).toBeEnabled();

    await user.click(exportButton);

    expect(clickSpy).toHaveBeenCalled();
    expect(screen.getByRole("status")).toHaveTextContent(
      "Export pack is being prepared.",
    );
  });

  it("shares an export pack with an accountant email", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(reportsFetchHandler());
    vi.stubGlobal("fetch", fetchImpl);
    const reportYear = currentReportYear();

    renderReportsScreen();

    await screen.findByText(`Profit & loss · Apr-Jun ${reportYear}`);
    await user.click(
      screen.getByRole("button", { name: "Share with accountant" }),
    );
    await user.type(
      screen.getByLabelText("Accountant email"),
      "accountant@example.test",
    );
    await user.click(screen.getByRole("button", { name: "Share" }));

    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalledWith(
        "/api/reports/share",
        expect.objectContaining({
          body: JSON.stringify({
            email: "accountant@example.test",
            period: {
              from: `${reportYear}-04-01`,
              to: `${reportYear}-06-30`,
            },
          }),
          method: "POST",
        }),
      );
    });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Export pack sent to accountant.",
    );
  });

  it("does not apply custom ranges while either date is empty", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(reportsFetchHandler());
    vi.stubGlobal("fetch", fetchImpl);
    const reportYear = currentReportYear();

    renderReportsScreen();

    await screen.findByText(`Profit & loss · Apr-Jun ${reportYear}`);
    fetchImpl.mockClear();

    await user.clear(screen.getByLabelText("From"));
    await user.click(screen.getByRole("button", { name: "Apply" }));

    expect(fetchImpl).not.toHaveBeenCalled();
    expect(
      screen.getByText(`Profit & loss · Apr-Jun ${reportYear}`),
    ).toBeInTheDocument();
  });

  it("keeps custom VAT ranges labelled and badged by the fetched quarter", async () => {
    const user = userEvent.setup();
    const reportYear = currentReportYear();
    vi.stubGlobal(
      "fetch",
      reportsFetch({
        calendar: calendarFixture([
          filingFixture({
            due_date: `${reportYear}-07-30`,
            key: "vat_return",
            label: "VAT return",
            status: "due-soon",
          }),
        ]),
      }),
    );

    renderReportsScreen();

    await screen.findByText(`Profit & loss · Apr-Jun ${reportYear}`);

    fireEvent.change(screen.getByLabelText("From"), {
      target: { value: `${reportYear}-05-01` },
    });
    fireEvent.change(screen.getByLabelText("To"), {
      target: { value: `${reportYear}-05-31` },
    });
    await user.click(screen.getByRole("button", { name: "Apply" }));

    expect(
      await screen.findByText(`Profit & loss · May-May ${reportYear}`),
    ).toBeInTheDocument();
    expect(
      screen.getByText(`VAT return · Apr-Jun ${reportYear}`),
    ).toBeInTheDocument();
    expect(screen.getByText("DUE 30 JUL")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Jan-Mar" }));

    expect(
      await screen.findByText(`VAT return · Jan-Mar ${reportYear}`),
    ).toBeInTheDocument();
    expect(screen.queryByText("DUE 30 JUL")).not.toBeInTheDocument();
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

  return async (input: RequestInfo | URL, init?: RequestInit) => {
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
    if (url.pathname === "/api/reports/share" && init?.method === "POST") {
      return jsonResponse({
        archive: {
          data_version: "test-version",
          generated_at: "2026-07-06T09:00:00Z",
          sha256: "abc123",
          size_bytes: 1024,
          url: "/api/identity/assets/export",
        },
        message: "Export pack sent to accountant.",
        status: "sent",
      });
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
    status: "registered",
  };
}

function notRegisteredVATFixture(): ReportsVAT {
  return {
    period: { from: "2026-04-01", to: "2026-06-30" },
    status: "not_registered",
  };
}

function calendarFixture(
  filings: ReportsFiling[] = defaultCalendarFilings(),
): ReportsFilingCalendar {
  return { filings };
}

function defaultCalendarFilings() {
  const year = currentReportYear();
  return [
    filingFixture({
      due_date: `${year}-07-30`,
      key: "vat_return",
      label: "VAT return",
      status: "due-soon",
    }),
    filingFixture({
      due_date: `${year}-08-14`,
      key: "annual_return",
      label: "Annual return",
      status: "upcoming",
    }),
    filingFixture({
      due_date: `${year + 1}-04-01`,
      key: "company_tax_return",
      label: "Company tax return",
      status: "upcoming",
    }),
    filingFixture({
      due_date: `${year}-10-06`,
      key: "personal_tax_return",
      label: "Personal tax return",
      status: "upcoming",
    }),
  ];
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

function currentReportYear() {
  return new Date().getUTCFullYear();
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
