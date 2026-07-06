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

async function mockReportsApi(
  page: Parameters<typeof test>[0]["page"],
  plRequests: string[],
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
      await fulfillJson(route, identityProfile());
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
    if (path === "/api/reports/vat") {
      await fulfillJson(route, vatFixture());
      return;
    }
    if (path === "/api/reports/calendar") {
      await fulfillJson(route, calendarFixture());
      return;
    }

    await fulfillJson(
      route,
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
    );
  });
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
