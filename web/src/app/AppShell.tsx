import { Suspense, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { NavLink, Outlet, useNavigate } from "react-router-dom";

import {
  getIdentityProfile,
  logoutIdentity,
  type IdentityUser,
} from "@/api/identity";
import { queryKeys } from "@/api/queryKeys";
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

export type AppShellProps = {
  readonly user: IdentityUser;
};

export function AppShell({ user }: AppShellProps) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [menuOpen, setMenuOpen] = useState(false);
  const [failedLogoUrl, setFailedLogoUrl] = useState<string | null>(null);
  const profileQuery = useQuery({
    queryFn: getIdentityProfile,
    queryKey: queryKeys.identity.profile(),
  });
  const profile = profileQuery.data;
  const companyName = profile?.trading_name ?? "Ledgerly";
  const logoUrl = profile?.logo_asset_url ?? undefined;
  const showLogo = !!logoUrl && failedLogoUrl !== logoUrl;
  const companyInitial = initialFor(companyName);
  const userInitial = initialFor(user.name || user.email);
  const logoutMutation = useMutation({
    mutationFn: logoutIdentity,
    onSettled: () => {
      queryClient.removeQueries({ queryKey: ["identity"] });
      navigate("/login", { replace: true });
    },
  });

  return (
    <div className="app-shell">
      <div className="app-shell__frame">
        <header className="app-shell__header">
          <div className="app-shell__brand" aria-label="Company identity">
            {showLogo ? (
              <img
                alt=""
                className="app-shell__logo-img"
                onError={() => {
                  if (logoUrl) {
                    setFailedLogoUrl(logoUrl);
                  }
                }}
                src={logoUrl}
              />
            ) : (
              <span className="app-shell__logo" aria-hidden="true">
                {companyInitial}
              </span>
            )}
            <span className="app-shell__company">{companyName}</span>
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
          <div className="app-shell-user">
            <span className="app-shell-user__context">IM · FY 2026-27</span>
            <button
              aria-expanded={menuOpen}
              aria-haspopup="menu"
              aria-label="Open account menu"
              className="app-shell-user__trigger"
              onClick={() => setMenuOpen((open) => !open)}
              type="button"
            >
              {userInitial}
            </button>
            {menuOpen ? (
              <div className="app-shell-user__menu" role="menu">
                <div className="app-shell-user__identity">
                  <strong>{user.name}</strong>
                  <span>{user.email}</span>
                </div>
                <button
                  disabled={logoutMutation.isPending}
                  onClick={() => logoutMutation.mutate()}
                  role="menuitem"
                  type="button"
                >
                  Logout
                </button>
              </div>
            ) : null}
          </div>
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

function initialFor(value: string) {
  const trimmed = value.trim();
  return (trimmed[0] ?? "L").toUpperCase();
}
