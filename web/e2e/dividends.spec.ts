import { expect, test, type Route } from "@playwright/test";

test("declares a dividend, refreshes history/previews, and reduces headroom", async ({
  page,
}) => {
  const state = dividendsState();
  await mockDividendsApi(page, state);

  await page.goto("/dividends");

  await expect(page.getByRole("heading", { name: "Dividends" }))
    .toBeVisible();
  await expect(page.getByLabel("Dividend headroom breakdown")).toContainText(
    "£17,160.00",
  );

  await page.getByLabel("Amount").fill("3000.00");
  await expect(page.getByText("Within headroom ✓")).toBeVisible();
  await expect(page.getByText("No WHT ✓")).toBeVisible();
  await expect(page.getByText("set aside personally £300.00")).toBeVisible();

  await page.getByRole("button", { name: "Generate voucher + minutes" })
    .click();

  const history = page.getByLabel("Dividend history");
  await expect(history.getByText("£3,000.00")).toBeVisible();
  await expect(history.getByText("£30.00")).toBeVisible();
  await expect(history.getByRole("link", { name: "Voucher" })).toBeVisible();
  await expect(history.getByRole("link", { name: "Minutes" })).toBeVisible();
  await expect(page.getByLabel("Dividend headroom breakdown")).toContainText(
    "£14,160.00",
  );
  await expect(
    page
      .frameLocator('iframe[title="Dividend voucher preview"]')
      .getByText("Dividend voucher"),
  ).toBeVisible();
  await expect(
    page
      .frameLocator('iframe[title="Board minutes preview"]')
      .getByText("Board minutes"),
  ).toBeVisible();

  await page.screenshot({
    fullPage: true,
    path: "test-results/dividends-screen-07.png",
  });
});

test("blocks an over-headroom dividend before declaration", async ({ page }) => {
  const state = dividendsState();
  await mockDividendsApi(page, state);

  await page.goto("/dividends");
  await page.getByLabel("Amount").fill("20000.00");

  await expect(page.getByRole("alert")).toContainText("Over headroom");
  await expect(page.getByRole("alert")).toContainText(
    "Distributable figure £17,160.00",
  );
  await expect(
    page.getByRole("button", { name: "Generate voucher + minutes" }),
  ).toBeDisabled();
  expect(state.declarations).toHaveLength(0);
});

test("DLA clear-with-dividend CTA lands on dividends prefilled and validated", async ({
  page,
}) => {
  const state = dividendsState();
  await mockDividendsApi(page, state);

  await page.goto("/dla");
  await expect(page.getByRole("status")).toContainText("£3,000.00 DR");

  await page.getByRole("button", { name: /Clear with dividend/ }).click();

  await expect(page).toHaveURL(/\/dividends\?amount=3000\.00$/);
  await expect(page.getByLabel("Amount")).toHaveValue("3000.00");
  await expect(page.getByText("Within headroom ✓")).toBeVisible();
});

async function mockDividendsApi(
  page: Parameters<typeof test>[0]["page"],
  state: DividendsState,
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
    if (path === "/api/dla/balance") {
      await fulfillJson(route, dlaBalance());
      return;
    }
    if (path === "/api/dla/ledger") {
      await fulfillJson(route, dlaLedger());
      return;
    }
    if (path === "/api/dividends/headroom") {
      await fulfillJson(route, currentHeadroom(state));
      return;
    }
    if (path === "/api/dividends/history") {
      await fulfillJson(route, { declarations: state.declarations });
      return;
    }
    if (path === "/api/dividends/validate" && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}");
      await fulfillJson(route, validationPayload(body.amount.amount, state));
      return;
    }
    if (path === "/api/dividends/declare" && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}");
      const declaration = declarationPayload(
        `dividend-${state.declarations.length + 1}`,
        body.amount.amount,
        currentHeadroom(state),
      );
      state.declarations = [declaration, ...state.declarations];
      await fulfillJson(route, declaration, 201);
      return;
    }
    const printMatch = path.match(
      /^\/api\/dividends\/declarations\/([^/]+)\/print$/,
    );
    if (printMatch) {
      const id = decodeURIComponent(printMatch[1]);
      const declaration = state.declarations.find((item) => item.id === id);
      await fulfillJson(
        route,
        declaration
          ? { declaration }
          : { status: 404, title: "Not Found", type: "about:blank" },
        declaration ? 200 : 404,
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

type Money = {
  amount: number;
  currency: "GBP";
};

type Headroom = {
  as_of: string;
  available: Money;
  distributable: boolean;
  financial_year: string;
  lines: Array<{ amount: Money; label: string }>;
};

type Declaration = ReturnType<typeof declarationPayload>;

type DividendsState = {
  declarations: Declaration[];
};

function dividendsState(): DividendsState {
  return {
    declarations: [],
  };
}

function currentHeadroom(state: DividendsState): Headroom {
  const declared = state.declarations.reduce(
    (sum, declaration) => sum + declaration.amount.amount,
    0,
  );
  const available = 1716000 - declared;
  return {
    as_of: "2026-07-06T00:00:00Z",
    available: gbp(available),
    distributable: available >= 0,
    financial_year: "2026-27",
    lines: [
      moneyLine("Retained earnings b/fwd", 1200000),
      moneyLine("Profit YTD (after expenses)", 516000),
      moneyLine("Corporation tax provision at 0%", 0),
      moneyLine("Dividends already declared YTD", -declared),
      moneyLine("Available to distribute", available),
    ],
  };
}

function validationPayload(amount: number, state: DividendsState) {
  const headroom = currentHeadroom(state);
  const within = headroom.distributable && amount <= headroom.available.amount;
  return {
    amount: gbp(amount),
    distributable: headroom.distributable,
    distributable_total: headroom.available,
    headroom,
    personal_tax: {
      marginal: gbp(Math.round(amount * 0.1)),
      message: `set aside personally ${formatGBP(Math.round(amount * 0.1))}`,
      prior_ytd: gbp(0),
      tax_year: "2026-27",
      with_dividend: gbp(amount),
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

function declarationPayload(id: string, amount: number, headroom: Headroom) {
  return {
    amount: gbp(amount),
    company_snapshot: {
      company_number: "137792C",
      director_name: "N. Meyer",
      legal_name: "NPM Limited",
      registered_office: {
        country: "Isle of Man",
        line1: "18 Athol St",
        line2: "",
        locality: "Douglas",
        postal_code: "IM1 1JA",
        region: "",
      },
      trading_name: "NPM Limited",
    },
    created_at: "2026-07-06T12:00:00Z",
    declared_date: "2026-07-06T00:00:00Z",
    headroom_snapshot: headroom,
    id,
    minutes_asset: "minutes-asset",
    per_share: gbp(amount / 100),
    shareholder_name: "N. Meyer",
    shareholder_snapshot: {
      class: "ordinary £1",
      name: "N. Meyer",
      shares: 100,
    },
    shares: 100,
    voucher_asset: "voucher-asset",
    withholding_snapshot: {
      note: "No dividend withholding tax is deducted under the active jurisdiction pack (withholding: none).",
      policy: "none",
      tax_year: "2026-27",
    },
  };
}

function identityProfile() {
  return {
    bank_details: {
      bank_name: "Revolut",
      bic: "REVOGB21",
      iban: "GB00REVO00000000000000",
    },
    company_number: "137792C",
    incorporation_date: "2024-04-03",
    legal_name: "NPM Limited",
    logo_asset_id: null,
    logo_asset_url: null,
    registered_office: {
      country: "Isle of Man",
      line1: "18 Athol St",
      line2: "",
      locality: "Douglas",
      postal_code: "IM1 1JA",
      region: "",
    },
    shareholders: [{ class: "ordinary £1", name: "N. Meyer", shares: 100 }],
    directors: [{ appointed_date: "2020-07-14", is_chair: true, name: "N. Meyer" }],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

function dlaBalance() {
  return {
    balance: dlaMoney(-300000),
    policy: {
      bik_warning_key: "benefit_in_kind_interest_free",
      credit_explainer_template:
        "You can repay yourself up to {{ balance }} at any time with no tax consequence.",
      credit_status_text: "In credit — tax-free to withdraw",
      overdrawn_warning_template:
        "Your loan account is {{ balance }} overdrawn. The Isle of Man has no UK-style s455 charge, but an interest-free loan can create a taxable benefit in kind - charge interest at the official rate or clear it with a dividend.",
      remedy: "clear_with_dividend",
      s455_charge: false,
    },
    status: "overdrawn",
    suggested_clearance: dlaMoney(300000),
  };
}

function dlaLedger() {
  return {
    entries: [
      {
        amount: dlaMoney(300000),
        balance_side: "DR",
        created_at: "2026-07-01T12:00:00Z",
        date: "2026-07-01",
        description: "Director drawing",
        drawn: dlaMoney(300000),
        id: 1,
        kind: "drawing",
        owed_to_you: dlaMoney(0),
        running_balance: dlaMoney(-300000),
        source_ref: "banking:drawing",
      },
    ],
    next_cursor: null,
  };
}

function moneyLine(label: string, amount: number) {
  return { amount: gbp(amount), label };
}

function gbp(amount: number): Money {
  return { amount, currency: "GBP" };
}

function dlaMoney(amountMinor: number) {
  return { amount_minor: amountMinor, currency: "GBP" };
}

async function fulfillJson(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    body: JSON.stringify(body),
    contentType: "application/json",
    status,
  });
}

function formatGBP(amount: number) {
  return new Intl.NumberFormat("en-GB", {
    currency: "GBP",
    style: "currency",
  }).format(amount / 100);
}
