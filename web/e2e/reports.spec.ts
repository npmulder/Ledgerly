import { expect, test, type Route } from "@playwright/test";

test("reports period switch refetches and seeded FX/CIT lines render", async ({
  page,
}) => {
  const plRequests: string[] = [];
  await mockReportsApi(page, plRequests);

  await page.goto("/reports");

  await expect(page.getByRole("heading", { name: "Reports" })).toBeVisible();
  await expect(page.getByText("Realised FX gains on settlement")).toBeVisible();
  await expect(page.getByText("IoM income tax at 0%")).toBeVisible();
  await expect(page.getByLabel("P&L lines").getByText("£0.00")).toBeVisible();
  expect(plRequests).toContain("/api/reports/pl?from=2026-04-01&to=2026-06-30");

  await page.getByRole("tab", { name: "Balance sheet" }).click();
  await expect(page.getByText("Balance sheet · 30 Jun 2026")).toBeVisible();
  await expect(
    page.getByLabel("Balance sheet sections").getByText("Cash GBP"),
  ).toBeVisible();
  await expect(page.getByText("Balanced")).toBeVisible();

  await page.getByRole("tab", { name: "Profit & loss" }).click();
  await expect(page.getByLabel("P&L lines")).toBeVisible();

  const refetch = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return (
      url.pathname === "/api/reports/pl" &&
      url.searchParams.get("from") === "2026-01-01" &&
      url.searchParams.get("to") === "2026-03-31"
    );
  });
  await page.getByRole("button", { name: "Jan-Mar" }).click();
  await refetch;

  expect(plRequests).toContain("/api/reports/pl?from=2026-01-01&to=2026-03-31");
});

test("reports shows neutral VAT note when not registered", async ({
  page,
}, testInfo) => {
  const plRequests: string[] = [];
  await mockReportsApi(page, plRequests, {
    calendar: nonVATCalendarFixture(),
    profile: identityProfile({ is_vat_registered: false }),
    vat: notRegisteredVATFixture(),
  });

  await page.goto("/reports");

  await expect(page.getByRole("heading", { name: "Reports" })).toBeVisible();
  await expect(page.getByText("Not VAT registered.")).toBeVisible();
  await expect(page.getByText("Box 1 · VAT due on sales")).toHaveCount(0);
  await expect(page.getByText("Box 4 · VAT reclaimed")).toHaveCount(0);
  await expect(page.getByText("Box 6 · Total sales ex-VAT")).toHaveCount(0);
  await expect(page.getByLabel("VAT return due-soon 30 JUL")).toHaveCount(0);

  await page.screenshot({
    fullPage: true,
    path: testInfo.outputPath("reports-not-vat-registered.png"),
  });
});

async function mockReportsApi(
  page: Parameters<typeof test>[0]["page"],
  plRequests: string[],
  overrides: {
    readonly calendar?: unknown;
    readonly profile?: unknown;
    readonly vat?: unknown;
  } = {},
) {
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
      await fulfillJson(route, overrides.profile ?? identityProfile());
      return;
    }
    if (path === "/api/reports/pl") {
      plRequests.push(`${path}${url.search}`);
      await fulfillJson(route, {
        ...plFixture(),
        period: {
          from: url.searchParams.get("from"),
          to: url.searchParams.get("to"),
        },
      });
      return;
    }
    if (path === "/api/reports/balance-sheet") {
      await fulfillJson(route, {
        ...balanceSheetFixture(),
        as_of: url.searchParams.get("asOf"),
      });
      return;
    }
    if (path === "/api/reports/vat") {
      await fulfillJson(route, overrides.vat ?? vatFixture());
      return;
    }
    if (path === "/api/reports/calendar") {
      await fulfillJson(route, overrides.calendar ?? calendarFixture());
      return;
    }

    await fulfillJson(
      route,
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
  });
}

function balanceSheetFixture() {
  return {
    as_of: "2026-06-30",
    assets: {
      label: "Assets",
      lines: [
        {
          account_code: "1000-cash-gbp",
          account_name: "Cash GBP",
          amount: money(1_588_350),
        },
      ],
      total: money(1_588_350),
    },
    balanced: true,
    equity: {
      label: "Equity",
      lines: [
        {
          account_code: "current-year-profit",
          account_name: "Current-year profit",
          amount: money(1_592_470),
        },
      ],
      total: money(1_592_470),
    },
    financial_year: "2026-27",
    liabilities: {
      label: "Liabilities",
      lines: [
        {
          account_code: "2200-vat-control",
          account_name: "VAT control",
          amount: money(-4_120),
        },
      ],
      total: money(-4_120),
    },
    total_assets: money(1_588_350),
    total_equity: money(1_592_470),
    total_liabilities: money(-4_120),
    total_liabilities_and_equity: money(1_588_350),
  };
}

function plFixture() {
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

function vatFixture() {
  return {
    box1: money(0),
    box4: money(4_120),
    box6: money(1_510_310),
    net_position: money(-4_120),
    period: { from: "2026-04-01", to: "2026-06-30" },
    status: "registered",
  };
}

function notRegisteredVATFixture() {
  return {
    period: { from: "2026-04-01", to: "2026-06-30" },
    status: "not_registered",
  };
}

function calendarFixture() {
  return {
    filings: [
      filingFixture({
        authority: "Isle of Man Customs & Excise",
        due_date: "2026-07-30",
        key: "vat_return",
        label: "VAT return",
        status: "due-soon",
      }),
      filingFixture({
        authority: "IoM Companies Registry",
        due_date: "2026-08-14",
        key: "annual_return",
        label: "Annual return",
        status: "upcoming",
      }),
      filingFixture({
        authority: "IoM Income Tax Division",
        due_date: "2027-04-01",
        key: "company_tax_return",
        label: "Company tax return",
        status: "upcoming",
      }),
      filingFixture({
        authority: "IoM Income Tax Division",
        due_date: "2026-10-06",
        key: "personal_tax_return",
        label: "Personal tax return",
        status: "upcoming",
      }),
    ],
  };
}

function filingFixture(overrides: Record<string, unknown>) {
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

function nonVATCalendarFixture() {
  return {
    filings: calendarFixture().filings.filter(
      (filing) => filing.key !== "vat_return",
    ),
  };
}

function identityProfile(overrides: Record<string, unknown> = {}) {
  return {
    bank_details: { bank_name: "", bic: "", iban: "" },
    company_number: "137792C",
    incorporation_date: "2020-07-14",
    is_vat_registered: true,
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
    directors: [
      { appointed_date: "2020-07-14", is_chair: true, name: "N. Meyer" },
    ],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
    ...overrides,
  };
}

function money(amount_minor: number) {
  return { amount_minor, currency: "GBP" };
}

async function fulfillJson(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    body: JSON.stringify(body),
    contentType: "application/json",
    status,
  });
}
