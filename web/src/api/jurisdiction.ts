import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type JurisdictionFilingDeadlines =
  components["schemas"]["JurisdictionFilingDeadlines"];
export type JurisdictionPack = components["schemas"]["JurisdictionPack"];
export type JurisdictionRuleSummary =
  components["schemas"]["JurisdictionRuleSummary"];

export function getJurisdictionPack() {
  return apiClient.get("/api/jurisdiction/pack");
}

export function getJurisdictionDeadlines() {
  return apiClient.get("/api/jurisdiction/deadlines");
}
