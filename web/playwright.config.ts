import { defineConfig, devices } from "@playwright/test";

const externalBaseURL = process.env.LEDGERLY_E2E_BASE_URL;
const baseURL = externalBaseURL ?? "http://127.0.0.1:8080";

export default defineConfig({
  expect: {
    timeout: 10_000,
  },
  fullyParallel: false,
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  reporter: process.env.CI ? "github" : "list",
  testDir: "./e2e",
  timeout: 60_000,
  use: {
    baseURL,
    trace: "on-first-retry",
  },
  webServer: externalBaseURL
    ? undefined
    : {
        command:
          "sh -c 'npm run build && cd .. && go run ./cmd/ledgerly migrate && go run ./cmd/ledgerly serve'",
        env: {
          ...process.env,
          LEDGERLY_DATABASE_URL:
            process.env.LEDGERLY_DATABASE_URL ??
            "postgres://postgres:postgres@localhost:5432/ledgerly_dev?sslmode=disable",
          LEDGERLY_DATA_DIR:
            process.env.LEDGERLY_DATA_DIR ?? "/tmp/ledgerly-playwright-data",
          LEDGERLY_ENV: process.env.LEDGERLY_ENV ?? "dev",
          LEDGERLY_HTTP_ADDR: "127.0.0.1:8080",
          LEDGERLY_LOG_LEVEL: process.env.LEDGERLY_LOG_LEVEL ?? "info",
        },
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        url: baseURL,
      },
});
