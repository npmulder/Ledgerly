import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type AdvisorCTA = components["schemas"]["AdvisorCTA"];
export type AdvisorInsight = components["schemas"]["AdvisorInsight"];
export type AdvisorInsightsResponse =
  components["schemas"]["AdvisorInsightsResponse"];
export type AdvisorRefreshResponse =
  components["schemas"]["AdvisorRefreshResponse"];
export type AdvisorSurface = components["schemas"]["AdvisorSurface"];

export function getAdvisorInsights(surface: AdvisorSurface) {
  return apiClient.get("/api/advisor/insights", {
    query: { surface },
  });
}

export function dismissAdvisorInsight(key: string) {
  return apiClient.post(advisorDismissPath(key));
}

export function refreshAdvisor() {
  return apiClient.post("/api/advisor/refresh");
}

function advisorDismissPath(key: string) {
  return `/api/advisor/insights/${encodeURIComponent(
    key,
  )}/dismiss` as "/api/advisor/insights/{key}/dismiss";
}
