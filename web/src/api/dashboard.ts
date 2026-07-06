import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type DashboardCash = components["schemas"]["DashboardCash"];
export type DashboardDLA = components["schemas"]["DashboardDLA"];
export type DashboardDividendHeadroom =
  components["schemas"]["DashboardDividendHeadroom"];
export type DashboardMoney = components["schemas"]["DashboardMoney"];
export type DashboardOutstanding =
  components["schemas"]["DashboardOutstanding"];
export type DashboardRate = components["schemas"]["DashboardRate"];
export type DashboardRecentInvoice =
  components["schemas"]["DashboardRecentInvoice"];
export type DashboardReviewQueueItem =
  components["schemas"]["DashboardReviewQueueItem"];
export type DashboardSummary = components["schemas"]["DashboardSummary"];
export type DashboardToReconcile =
  components["schemas"]["DashboardToReconcile"];

export function getDashboardSummary() {
  return apiClient.get("/api/dashboard/summary");
}
