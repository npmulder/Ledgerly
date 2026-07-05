import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it } from "vitest";

import { App } from "@/app/App";

afterEach(() => {
  cleanup();
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
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
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
      expect(
        screen.getByRole("link", { name: "Settings" }),
      ).toHaveClass("app-shell-nav__link--active");
    },
  );

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
});
