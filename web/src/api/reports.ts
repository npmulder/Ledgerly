import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type ReportsBalanceSheet =
  components["schemas"]["ReportsBalanceSheetResponse"];
export type ReportsFiling = components["schemas"]["ReportsFiling"];
export type ReportsFilingCalendar =
  components["schemas"]["ReportsFilingCalendarResponse"];
export type ReportsExpenses = components["schemas"]["ReportsExpensesResponse"];
export type ReportsMoney = components["schemas"]["ReportsMoney"];
export type ReportsPL = components["schemas"]["ReportsPLResponse"];
export type ReportsShareResponse =
  components["schemas"]["ReportsShareResponse"];
export type ReportsVAT = components["schemas"]["ReportsVATResponse"];

export type ReportsPLPrintPayload = {
  readonly app_version: string;
  readonly company_name: string;
  readonly generated_at: string;
  readonly pl: ReportsPL;
};

export function getReportsPL(from: string, to: string) {
  return apiClient.get("/api/reports/pl", {
    query: { from, to },
  });
}

export function getReportsExpenses(from: string, to: string) {
  return apiClient.get("/api/reports/expenses", {
    query: { from, to },
  });
}

export function getReportsBalanceSheet(asOf: string) {
  return apiClient.get("/api/reports/balance-sheet", {
    query: { asOf },
  });
}

export function getReportsVAT(period: string) {
  return apiClient.get("/api/reports/vat", {
    query: { period },
  });
}

export function getReportsCalendar() {
  return apiClient.get("/api/reports/calendar");
}

export function getReportsProfitYTD(taxYear: string) {
  return apiClient.get("/api/reports/profit-ytd", {
    query: { taxYear },
  });
}

export function reportsExportURL(from: string, to: string) {
  const params = new URLSearchParams({ from, to });
  return `/api/reports/export?${params.toString()}`;
}

export function reportsExpensesCSVURL(from: string, to: string) {
  const params = new URLSearchParams({ from, to });
  return `/api/reports/expenses.csv?${params.toString()}`;
}

export function shareReportsExport(email: string, from: string, to: string) {
  return apiClient.post("/api/reports/share", {
    email,
    period: { from, to },
  });
}

export async function getReportsPLPrintPayload(periodID: string) {
  const [from, to] = periodID.split("_");
  if (!from || !to) {
    throw new Error("Invalid reports print period");
  }
  const pl = await getReportsPL(from, to);
  return {
    app_version: "",
    company_name: "Ledgerly",
    generated_at: new Date().toISOString(),
    pl,
  } satisfies ReportsPLPrintPayload;
}
