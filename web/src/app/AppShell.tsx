import { Suspense } from "react";
import { NavLink, Outlet } from "react-router-dom";

import { PageTitle, Screen } from "@/components";

const primaryNavItems = [
  { end: true, label: "Dashboard", to: "/" },
  { end: false, label: "Invoices", to: "/invoices" },
  { end: false, label: "Banking", to: "/banking" },
  { end: false, label: "DLA", to: "/dla" },
  { end: false, label: "Dividends", to: "/dividends" },
  { end: false, label: "Reports", to: "/reports" },
  { end: false, label: "Settings", to: "/settings" },
] as const;

export function AppShell() {
  return (
    <div className="app-shell">
      <div className="app-shell__frame">
        <header className="app-shell__header">
          <div className="app-shell__brand" aria-label="Company identity">
            <span className="app-shell__logo" aria-hidden="true">
              K
            </span>
            <span className="app-shell__company">NPM Limited</span>
          </div>
          <nav className="app-shell-nav" aria-label="Primary">
            {primaryNavItems.map((item) => (
              <NavLink
                className={({ isActive }) =>
                  isActive
                    ? "app-shell-nav__link app-shell-nav__link--active"
                    : "app-shell-nav__link"
                }
                end={item.end}
                key={item.to}
                to={item.to}
              >
                {item.label}
              </NavLink>
            ))}
          </nav>
        </header>
        <Screen>
          <Suspense fallback={<PageTitle>Loading</PageTitle>}>
            <Outlet />
          </Suspense>
        </Screen>
      </div>
    </div>
  );
}
