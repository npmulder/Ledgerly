import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { AdvisorInsight } from "@/api/advisor";
import { AdvisorPanel, AdvisorStrip } from "@/components/Advisor";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Advisor components", () => {
  it("orders amber before teal, then by recency, and marks severity for styling", async () => {
    vi.stubGlobal(
      "fetch",
      advisorFetch([
        advisorInsight({
          created_at: "2026-07-06T12:00:00Z",
          key: "teal-new",
          rendered_text: "Teal newer insight",
          severity: "teal",
        }),
        advisorInsight({
          created_at: "2026-07-06T09:00:00Z",
          key: "amber-old",
          rendered_text: "Amber older insight",
          severity: "amber",
        }),
        advisorInsight({
          created_at: "2026-07-06T11:00:00Z",
          key: "amber-new",
          rendered_text: "Amber newer insight",
          severity: "amber",
        }),
      ]),
    );

    renderAdvisor(<AdvisorPanel surface="dashboard" />);

    const panel = await screen.findByRole("region", {
      name: "Advisor panel",
    });
    await within(panel).findByText("Amber newer insight");
    const rows = panel.querySelectorAll(".advisor-insight-row");

    expect([...rows].map((row) => row.textContent)).toEqual([
      expect.stringContaining("Amber newer insight"),
      expect.stringContaining("Amber older insight"),
      expect.stringContaining("Teal newer insight"),
    ]);
    expect(rows[0]).toHaveAttribute("data-severity", "amber");
    expect(rows[2]).toHaveAttribute("data-severity", "teal");
    expect(panel).toHaveClass("advisor-panel");
  });

  it("dispatches invoice reminder CTAs and shows the result toast", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://localhost");
        if (url.pathname === "/api/advisor/insights") {
          return jsonResponse({
            insights: [
              advisorInsight({
                cta: {
                  action: "invoicing.sendReminder",
                  label: "Send reminder",
                  params: { invoice_id: "invoice-overdue" },
                },
                rendered_text: "Invoice INV-1 is 9 days overdue.",
              }),
            ],
          });
        }
        if (
          url.pathname === "/api/invoicing/invoices/invoice-overdue/remind" &&
          init?.method === "POST"
        ) {
          return jsonResponse({
            invoice: { id: "invoice-overdue", number: "INV-1" },
            reminder: {
              invoice_id: "invoice-overdue",
              sent_at: "2026-07-06T12:00:00Z",
            },
          });
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAdvisor(<AdvisorStrip surface="invoices" />);

    await user.click(
      await screen.findByRole("button", { name: "Send reminder" }),
    );

    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalledWith(
        "/api/invoicing/invoices/invoice-overdue/remind",
        expect.objectContaining({ method: "POST" }),
      );
    });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Reminder sent for INV-1.",
    );
  });

  it("dispatches navigate CTAs and normalizes dividends amount prefill", async () => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      advisorFetch([
        advisorInsight({
          cta: {
            action: "navigate:/dividends?amount=300000",
            label: "Clear with dividend",
          },
          rendered_text: "DLA is overdrawn.",
        }),
      ]),
    );

    renderAdvisor(
      <Routes>
        <Route path="/" element={<AdvisorStrip surface="dla" />} />
        <Route path="/dividends" element={<LocationSearch />} />
      </Routes>,
    );

    await user.click(
      await screen.findByRole("button", { name: "Clear with dividend" }),
    );

    expect(await screen.findByText("Search: ?amount=3000.00")).toBeVisible();
  });

  it.each([
    ["reports.openFilingCalendar", "Open filing calendar", "/reports"],
    ["dividends.open", "Open dividends", "/dividends"],
  ])("dispatches current pack CTA %s", async (action, label, pathname) => {
    const user = userEvent.setup();
    vi.stubGlobal(
      "fetch",
      advisorFetch([
        advisorInsight({
          cta: { action, label },
          rendered_text: `${label} insight.`,
        }),
      ]),
    );

    renderAdvisor(
      <Routes>
        <Route path="/" element={<AdvisorStrip surface="dashboard" />} />
        <Route path={pathname} element={<LocationPath />} />
      </Routes>,
    );

    await user.click(await screen.findByRole("button", { name: label }));

    expect(await screen.findByText(`Path: ${pathname}`)).toBeVisible();
  });

  it("dispatches the current moneyfx refresh CTA", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://localhost");
        if (url.pathname === "/api/advisor/insights") {
          return jsonResponse({
            insights: [
              advisorInsight({
                cta: {
                  action: "moneyfx.refreshRates",
                  label: "Refresh rates",
                },
                rendered_text: "ECB rates are stale.",
              }),
            ],
          });
        }
        if (
          url.pathname === "/api/moneyfx/rates/today" &&
          url.searchParams.get("from") === "EUR" &&
          url.searchParams.get("to") === "GBP"
        ) {
          return jsonResponse({
            fetched_at: "2026-07-06T12:00:00Z",
            from: "EUR",
            rate: "0.8600",
            rate_date: "2026-07-05",
            source: "ECB",
            to: "GBP",
          });
        }
        if (
          url.pathname === "/api/advisor/refresh" &&
          init?.method === "POST"
        ) {
          return jsonResponse({
            run: {
              completed_at: "2026-07-06T12:00:01Z",
              id: "advisor-run",
              started_at: "2026-07-06T12:00:00Z",
              status: "completed",
            },
          });
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAdvisor(<AdvisorStrip surface="banking" />);

    await user.click(
      await screen.findByRole("button", { name: "Refresh rates" }),
    );

    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalledWith(
        "/api/moneyfx/rates/today?from=EUR&to=GBP",
        expect.objectContaining({ method: "GET" }),
      );
      expect(fetchImpl).toHaveBeenCalledWith(
        "/api/advisor/refresh",
        expect.objectContaining({ method: "POST" }),
      );
    });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Rates checked.",
    );
  });

  it("hides CTA buttons for unknown actions", async () => {
    vi.stubGlobal(
      "fetch",
      advisorFetch([
        advisorInsight({
          cta: {
            action: "future.action",
            label: "Open future action",
          },
          rendered_text: "Forward compatible insight.",
        }),
      ]),
    );

    renderAdvisor(<AdvisorStrip surface="reports" />);

    expect(
      await screen.findByText("Forward compatible insight."),
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Open future action" }),
    ).not.toBeInTheDocument();
  });

  it("removes dismissed insights optimistically and rolls back on error", async () => {
    const user = userEvent.setup();
    const dismiss = deferred<Response>();
    const insight = advisorInsight({
      key: "rollback-key",
      rendered_text: "Rollback insight",
    });
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), "http://localhost");
        if (url.pathname === "/api/advisor/insights") {
          return Promise.resolve(jsonResponse({ insights: [insight] }));
        }
        if (
          url.pathname === "/api/advisor/insights/rollback-key/dismiss" &&
          init?.method === "POST"
        ) {
          return dismiss.promise;
        }
        return Promise.resolve(
          jsonResponse(
            { status: 404, title: "Not Found", type: "about:blank" },
            404,
            "application/problem+json",
          ),
        );
      }),
    );

    renderAdvisor(<AdvisorStrip surface="dashboard" />);

    const strip = await screen.findByRole("region", {
      name: "Dashboard advisor",
    });
    await user.click(
      within(strip).getByRole("button", {
        name: /Dismiss advisor insight: Rollback insight/,
      }),
    );

    await waitFor(() => {
      expect(screen.queryByText("Rollback insight")).not.toBeInTheDocument();
    });

    dismiss.resolve(
      jsonResponse(
        {
          detail: "forced failure",
          status: 500,
          title: "Internal Server Error",
          type: "about:blank",
        },
        500,
        "application/problem+json",
      ),
    );

    expect(await screen.findByText("Rollback insight")).toBeVisible();
  });
});

function renderAdvisor(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });

  render(
    <MemoryRouter initialEntries={["/"]}>
      <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
    </MemoryRouter>,
  );
}

function LocationSearch() {
  const location = useLocation();
  return <p>Search: {location.search}</p>;
}

function LocationPath() {
  const location = useLocation();
  return <p>Path: {location.pathname}</p>;
}

function advisorFetch(insights: AdvisorInsight[]) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/advisor/insights") {
      return jsonResponse({ insights });
    }
    return jsonResponse(
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
      "application/problem+json",
    );
  });
}

function advisorInsight(
  overrides: Partial<AdvisorInsight> = {},
): AdvisorInsight {
  return {
    bindings: {},
    created_at: "2026-07-06T10:00:00Z",
    cta: {
      action: "navigate:/reports",
      label: "Open",
    },
    key: "advisor-key",
    rendered_text: "Advisor insight",
    rule_id: "advisor-rule",
    severity: "amber",
    surfaces: ["dashboard"],
    ...overrides,
  };
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

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, reject, resolve };
}
