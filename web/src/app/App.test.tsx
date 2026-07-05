import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "@/app/App";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const topLevelRoutes = [
  { activeNav: "Dashboard", path: "/", title: "Dashboard" },
  { activeNav: "Invoices", path: "/invoices", title: "Invoices" },
  {
    activeNav: "Invoices",
    path: "/invoices/INV-2026-07",
    title: "Invoice editor",
  },
  { activeNav: "Banking", path: "/banking", title: "Banking" },
  { activeNav: "DLA", path: "/dla", title: "DLA" },
  { activeNav: "Dividends", path: "/dividends", title: "Dividends" },
  { activeNav: "Reports", path: "/reports", title: "Reports" },
  { activeNav: "Settings", path: "/settings", title: "Company" },
] as const;

const settingsRoutes = [
  { path: "/settings/company", title: "Company" },
  { path: "/settings/jurisdiction", title: "Jurisdiction" },
  { path: "/settings/clients", title: "Clients" },
  { path: "/settings/invoicing-defaults", title: "Invoicing defaults" },
  { path: "/settings/bank-connections", title: "Bank connections" },
  { path: "/settings/users", title: "Users" },
] as const;

function renderAt(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  vi.stubGlobal("fetch", authenticatedFetch());

  render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function renderAtUnauthenticated(path: string) {
  renderAtWithFetch(path, unauthenticatedFetch());
}

function renderAtWithFetch(path: string, fetchImpl: typeof fetch) {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  vi.stubGlobal("fetch", fetchImpl);

  render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("App routing shell", () => {
  it.each(topLevelRoutes)(
    "renders shell, page title, and active nav for $path",
    async ({ activeNav, path, title }) => {
      renderAt(path);

      expect(
        await screen.findByRole("heading", { level: 1, name: title }),
      ).toBeInTheDocument();
      expect(screen.getByRole("banner")).toBeInTheDocument();
      expect(screen.getByText("NPM Limited")).toBeInTheDocument();

      const primaryNav = screen.getByRole("navigation", { name: "Primary" });
      expect(
        within(primaryNav).getByRole("link", { name: activeNav }),
      ).toHaveClass("app-shell-nav__link--active");
    },
  );

  it.each(settingsRoutes)(
    "renders settings sub-shell and active settings link for $path",
    async ({ path, title }) => {
      renderAt(path);

      expect(
        await screen.findByRole("heading", { level: 1, name: title }),
      ).toBeInTheDocument();

      expect(
        screen.getByRole("navigation", { name: "Primary" }),
      ).toBeInTheDocument();
      const settingsNav = screen.getByRole("navigation", {
        name: "Settings",
      });
      expect(
        within(settingsNav).getByRole("link", { name: title }),
      ).toHaveClass("settings-shell__link--active");
      expect(screen.getByRole("link", { name: "Settings" })).toHaveClass(
        "app-shell-nav__link--active",
      );
    },
  );

  it("redirects protected routes to login when the session is missing", async () => {
    renderAtUnauthenticated("/settings/company");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Login" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("banner")).not.toBeInTheDocument();
  });

  it("renders a shell 404 for unknown app routes", async () => {
    renderAt("/missing-screen");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Page not found",
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("banner")).toBeInTheDocument();
  });

  it("keeps print routes outside the app shell", async () => {
    renderAt("/print/invoices/INV-2026-07");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Print document",
      }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("banner")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("navigation", { name: "Primary" }),
    ).not.toBeInTheDocument();
  });

  it("keeps the session visible when logout fails", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const path = pathFromRequest(input);
      if (path === "/api/identity/me") {
        return jsonResponse({
          created_at: "2026-07-05T12:00:00Z",
          email: "owner@example.com",
          id: 1,
          name: "N. Meyer",
        });
      }
      if (path === "/api/identity/profile") {
        return jsonResponse(identityProfile());
      }
      if (path === "/api/identity/logout") {
        return jsonResponse(
          {
            detail: "session cookie was not cleared",
            status: 503,
            title: "Service unavailable",
            type: "about:blank",
          },
          503,
          "application/problem+json",
        );
      }
      return jsonResponse(
        {
          status: 404,
          title: "Not Found",
          type: "about:blank",
        },
        404,
        "application/problem+json",
      );
    });
    renderAtWithFetch("/", fetchImpl);

    expect(
      await screen.findByRole("heading", { level: 1, name: "Dashboard" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open account menu" }));
    await user.click(screen.getByRole("menuitem", { name: "Logout" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Service unavailable",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "session cookie was not cleared",
    );
    expect(screen.getByRole("banner")).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { level: 1, name: "Login" }),
    ).not.toBeInTheDocument();
  });
});

function authenticatedFetch() {
  return vi.fn(async (input: RequestInfo | URL) => {
    const path = pathFromRequest(input);
    if (path === "/api/identity/me") {
      return jsonResponse({
        created_at: "2026-07-05T12:00:00Z",
        email: "owner@example.com",
        id: 1,
        name: "N. Meyer",
      });
    }
    if (path === "/api/identity/profile") {
      return jsonResponse(identityProfile());
    }
    if (path === "/api/invoicing/clients") {
      return jsonResponse({ clients: [] });
    }
    return jsonResponse(
      {
        status: 404,
        title: "Not Found",
        type: "about:blank",
      },
      404,
      "application/problem+json",
    );
  });
}

function unauthenticatedFetch() {
  return vi.fn(async () =>
    jsonResponse(
      {
        detail: "authentication required",
        status: 401,
        title: "Unauthorized",
        type: "about:blank",
      },
      401,
      "application/problem+json",
    ),
  );
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
    shareholders: [
      {
        class: "ordinary £1",
        name: "N. Meyer",
        shares: 100,
      },
    ],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

function pathFromRequest(input: RequestInfo | URL) {
  if (input instanceof Request) {
    return new URL(input.url, "http://localhost").pathname;
  }

  return new URL(String(input), "http://localhost").pathname;
}

function jsonResponse(
  body: unknown,
  status = 200,
  contentType = "application/json",
) {
  return new Response(JSON.stringify(body), {
    headers: {
      "Content-Type": contentType,
    },
    status,
  });
}
