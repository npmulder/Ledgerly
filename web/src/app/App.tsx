import { lazy, Suspense } from "react";
import { Route, Routes } from "react-router-dom";

import { AppShell } from "@/app/AppShell";
import { DevApiScreen } from "@/screens/DevApiScreen";
import { DevComponentsScreen } from "@/screens/DevComponentsScreen";
import { DevTokensScreen } from "@/screens/DevTokensScreen";
import { LoginScreen } from "@/screens/LoginScreen";

const DashboardScreen = lazy(() =>
  import("@/screens/DashboardScreen").then((module) => ({
    default: module.DashboardScreen,
  })),
);
const InvoicesScreen = lazy(() =>
  import("@/screens/InvoicesScreen").then((module) => ({
    default: module.InvoicesScreen,
  })),
);
const InvoiceEditorScreen = lazy(() =>
  import("@/screens/InvoiceEditorScreen").then((module) => ({
    default: module.InvoiceEditorScreen,
  })),
);
const BankingScreen = lazy(() =>
  import("@/screens/BankingScreen").then((module) => ({
    default: module.BankingScreen,
  })),
);
const DlaScreen = lazy(() =>
  import("@/screens/DlaScreen").then((module) => ({
    default: module.DlaScreen,
  })),
);
const DividendsScreen = lazy(() =>
  import("@/screens/DividendsScreen").then((module) => ({
    default: module.DividendsScreen,
  })),
);
const ReportsScreen = lazy(() =>
  import("@/screens/ReportsScreen").then((module) => ({
    default: module.ReportsScreen,
  })),
);
const SettingsScreen = lazy(() =>
  import("@/screens/SettingsScreen").then((module) => ({
    default: module.SettingsScreen,
  })),
);
const NotFoundScreen = lazy(() =>
  import("@/screens/NotFoundScreen").then((module) => ({
    default: module.NotFoundScreen,
  })),
);
const PrintRouteScreen = lazy(() =>
  import("@/screens/PrintRouteScreen").then((module) => ({
    default: module.PrintRouteScreen,
  })),
);

export function App() {
  return (
    <Routes>
      <Route
        path="/print/*"
        element={
          <Suspense fallback={null}>
            <PrintRouteScreen />
          </Suspense>
        }
      />
      <Route element={<AppShell />}>
        <Route index element={<DashboardScreen />} />
        <Route path="/invoices" element={<InvoicesScreen />} />
        <Route path="/invoices/:id" element={<InvoiceEditorScreen />} />
        <Route path="/banking" element={<BankingScreen />} />
        <Route path="/dla" element={<DlaScreen />} />
        <Route path="/dividends" element={<DividendsScreen />} />
        <Route path="/reports" element={<ReportsScreen />} />
        <Route path="/settings/*" element={<SettingsScreen />} />
        <Route path="*" element={<NotFoundScreen />} />
      </Route>
      <Route path="/dev/api" element={<DevApiScreen />} />
      <Route path="/dev/components" element={<DevComponentsScreen />} />
      <Route path="/dev/tokens" element={<DevTokensScreen />} />
      <Route path="/login" element={<LoginScreen />} />
    </Routes>
  );
}
