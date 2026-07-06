import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type MoneyFXRateResponse = components["schemas"]["MoneyFXRateResponse"];

export function getTodayRate(from: string, to: string) {
  return apiClient.get("/api/moneyfx/rates/today", {
    query: { from, to },
  });
}
