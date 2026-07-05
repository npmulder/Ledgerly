import { expect, test } from "@playwright/test";

const ownerEmail = process.env.LEDGERLY_E2E_EMAIL ?? "owner@example.com";
const ownerPassword =
  process.env.LEDGERLY_E2E_PASSWORD ?? "correct horse battery staple";

test.beforeEach(async ({ request }) => {
  const response = await request.post("/api/identity/register", {
    data: {
      email: ownerEmail,
      name: "N. Meyer",
      password: ownerPassword,
    },
  });

  expect([201, 403]).toContain(response.status());
});

test("login, edit company trading name, and update header", async ({
  page,
}) => {
  const tradingName = `NPM Smoke ${Date.now()}`;

  await page.goto("/settings/company");
  await expect(page.getByRole("heading", { name: "Login" })).toBeVisible();

  await page.getByLabel("Email").fill(ownerEmail);
  await page.getByLabel("Password").fill(ownerPassword);
  await page.getByRole("button", { name: "Login" }).click();

  await expect(page.getByRole("heading", { name: "Company" })).toBeVisible();
  await page.getByLabel("Trading name").fill(tradingName);
  await page.getByRole("button", { name: "Save changes" }).click();

  await expect(page.getByRole("banner").getByText(tradingName)).toBeVisible();

  await page.reload();

  await expect(page.getByRole("banner").getByText(tradingName)).toBeVisible();
  await expect(page.getByLabel("Trading name")).toHaveValue(tradingName);
});
