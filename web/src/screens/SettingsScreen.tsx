import { NavLink, Route, Routes } from "react-router-dom";

import { PageTitle } from "@/components";

const settingsItems = [
  { label: "Company", title: "Company", to: "/settings/company" },
  {
    label: "Jurisdiction",
    title: "Jurisdiction",
    to: "/settings/jurisdiction",
  },
  { label: "Clients", title: "Clients", to: "/settings/clients" },
  {
    label: "Invoicing defaults",
    title: "Invoicing defaults",
    to: "/settings/invoicing-defaults",
  },
  {
    label: "Bank connections",
    title: "Bank connections",
    to: "/settings/bank-connections",
  },
  { label: "Users", title: "Users", to: "/settings/users" },
] as const;

function SettingsTitle({ title }: { title: string }) {
  return <PageTitle id="settings-page-title">{title}</PageTitle>;
}

export function SettingsScreen() {
  return (
    <div className="settings-shell">
      <nav className="settings-shell__nav" aria-label="Settings">
        {settingsItems.map((item) => (
          <NavLink
            className={({ isActive }) =>
              isActive
                ? "settings-shell__link settings-shell__link--active"
                : "settings-shell__link"
            }
            key={item.to}
            to={item.to}
          >
            {item.label}
          </NavLink>
        ))}
      </nav>
      <section
        className="settings-shell__content"
        aria-labelledby="settings-page-title"
      >
        <Routes>
          <Route index element={<SettingsTitle title="Company" />} />
          {settingsItems.map((item) => (
            <Route
              element={<SettingsTitle title={item.title} />}
              key={item.to}
              path={item.to.replace("/settings/", "")}
            />
          ))}
          <Route
            path="*"
            element={<SettingsTitle title="Settings not found" />}
          />
        </Routes>
      </section>
    </div>
  );
}
