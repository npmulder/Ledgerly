import { expect, test, type Route } from "@playwright/test";

test("settings company act suggestion and advisor strip", async ({ page }) => {
  let profile = identityProfile();
  let savedActType: string | null | undefined;

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;

    if (!path.startsWith("/api/")) {
      await route.fallback();
      return;
    }

    if (path === "/api/identity/me") {
      await fulfillJSON(route, {
        created_at: "2026-07-05T12:00:00Z",
        email: "owner@example.com",
        id: 1,
        name: "N. Meyer",
      });
      return;
    }

    if (path === "/api/jurisdiction/pack") {
      await fulfillJSON(route, jurisdictionPack());
      return;
    }

    if (path === "/api/advisor/insights") {
      await fulfillJSON(route, {
        insights: [
          {
            bindings: {
              act_name: "Companies Act 1931",
              director_count: 1,
              minimum_directors: 2,
            },
            created_at: "2026-07-08T06:00:00Z",
            cta: {
              action: "navigate:/settings/company",
              label: "Open company settings",
              params: {},
            },
            key: "company_minimum_directors:test",
            rendered_text:
              "Your company is registered under the Companies Act 1931 and must have at least 2 directors - profile lists 1.",
            rule_id: "company_minimum_directors",
            severity: "amber",
            surfaces: ["dashboard", "settings"],
          },
        ],
      });
      return;
    }

    if (path === "/api/audit/history/identity/profile/1") {
      await fulfillJSON(route, { entries: [] });
      return;
    }

    if (path === "/api/identity/profile" && request.method() === "PATCH") {
      const patch = request.postDataJSON();
      savedActType = patch.act_type;
      profile = {
        ...profile,
        act_type:
          patch.act_type === undefined ? profile.act_type : patch.act_type,
        company_number: patch.company_number ?? profile.company_number,
      };
      await fulfillJSON(route, profile);
      return;
    }

    if (path === "/api/identity/profile") {
      await fulfillJSON(route, profile);
      return;
    }

    await fulfillJSON(route, { status: 404, title: "Not Found" }, 404);
  });

  await page.goto("/");
  await page.evaluate(() => {
    window.history.pushState(null, "", "/settings/company");
    window.dispatchEvent(new PopStateEvent("popstate"));
  });

  await expect(page.getByRole("heading", { name: "Company" })).toBeVisible();
  await expect(page.getByLabel("Settings advisor")).toContainText(
    "must have at least 2 directors",
  );
  await expect(page.getByLabel("Company Act")).toHaveValue("");
  await expect(
    page.getByText("Suggested from company number: Companies Act 1931."),
  ).toBeVisible();

  await page.getByLabel("Company Act").selectOption("companies-act-1931");
  await page.getByRole("button", { name: "Save changes" }).click();

  await expect.poll(() => savedActType).toBe("companies-act-1931");
  await expect(page.getByLabel("Company Act")).toHaveValue(
    "companies-act-1931",
  );
});

async function fulfillJSON(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    body: JSON.stringify(body),
    contentType: "application/json",
    status,
  });
}

function identityProfile() {
  return {
    act_type: null,
    bank_details: { bank_name: "", bic: "", iban: "" },
    company_number: "137792C",
    directors: [
      {
        appointed_date: "2020-07-14",
        is_chair: true,
        name: "N. Meyer",
      },
    ],
    incorporation_date: "2020-07-14",
    is_vat_registered: false,
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
    shareholders: [{ class: "ordinary GBP1", name: "N. Meyer", shares: 100 }],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

function jurisdictionPack() {
  return {
    company_acts: [
      {
        act_type: "companies-act-1931",
        company_number_suffixes: ["C"],
        corporate_directors: false,
        label: "Companies Act 1931",
        minimum_directors: 2,
      },
      {
        act_type: "companies-act-2006",
        company_number_suffixes: ["V"],
        corporate_directors: null,
        label: "Companies Act 2006",
        minimum_directors: 1,
      },
    ],
    meta: {
      currency: "GBP",
      id: "isle-of-man",
      name: "Isle of Man",
      version: "1.0",
    },
    rule_summaries: [],
  };
}
