import { lazy, Suspense } from "react";
import { useQuery } from "@tanstack/react-query";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";

import { isApiError } from "@/api/client";
import { getCurrentUser } from "@/api/identity";
import { queryKeys } from "@/api/queryKeys";
import { AppShell } from "@/app/AppShell";
import { PageTitle, Screen } from "@/components";
import { DevApiScreen } from "@/screens/DevApiScreen";
import { DevComponentsScreen } from "@/screens/DevComponentsScreen";
import { DevTokensScreen } from "@/screens/DevTokensScreen";
import { LoginScreen } from "@/screens/LoginScreen";
import { RegisterScreen } from "@/screens/RegisterScreen";

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
const BankingPayeeRulesScreen = lazy(() =>
  import("@/screens/BankingPayeeRulesScreen").then((module) => ({
    default: module.BankingPayeeRulesScreen,
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
      <Route element={<AuthenticatedShell />}>
        <Route index element={<DashboardScreen />} />
        <Route path="/invoices" element={<InvoicesScreen />} />
        <Route path="/invoices/:id" element={<InvoiceEditorScreen />} />
        <Route path="/banking" element={<BankingScreen />} />
        <Route
          path="/banking/payee-rules"
          element={<BankingPayeeRulesScreen />}
        />
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
      <Route path="/register" element={<RegisterScreen />} />
    </Routes>
  );
}

function AuthenticatedShell() {
  const location = useLocation();
  const userQuery = useQuery({
    queryFn: getCurrentUser,
    queryKey: queryKeys.identity.me(),
    retry: false,
  });

  if (userQuery.isPending) {
    return (
      <Screen>
        <PageTitle>Loading</PageTitle>
      </Screen>
    );
  }

  if (userQuery.isError) {
    if (isApiError(userQuery.error) && userQuery.error.status === 401) {
      return <Navigate replace state={{ from: location }} to="/login" />;
    }

    return (
      <Screen>
        <PageTitle>Unable to load session</PageTitle>
      </Screen>
    );
  }

  return <AppShell user={userQuery.data} />;
}
