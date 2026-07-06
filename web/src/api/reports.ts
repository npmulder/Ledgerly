import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type ReportsFiling = components["schemas"]["ReportsFiling"];
export type ReportsFilingCalendar =
  components["schemas"]["ReportsFilingCalendarResponse"];
export type ReportsMoney = components["schemas"]["ReportsMoney"];
export type ReportsPL = components["schemas"]["ReportsPLResponse"];
export type ReportsVAT = components["schemas"]["ReportsVATResponse"];

export function getReportsPL(from: string, to: string) {
  return apiClient.get("/api/reports/pl", {
    query: { from, to },
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
