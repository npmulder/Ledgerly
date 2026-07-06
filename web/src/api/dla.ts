import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type DLABalance = components["schemas"]["DLABalanceResponse"];
export type DLAEntryRequest = components["schemas"]["DLAEntryRequest"];
export type DLAEntry = components["schemas"]["DLALedgerEntry"];
export type DLALedger = components["schemas"]["DLALedgerResponse"];
export type DLAMoney = components["schemas"]["DLAMoney"];

export function getDLABalance() {
  return apiClient.get("/api/dla/balance");
}

export function getDLALedger(cursor?: string | null) {
  return apiClient.get("/api/dla/ledger", {
    query: cursor ? { cursor } : undefined,
  });
}

export function createDLAEntry(input: DLAEntryRequest) {
  return apiClient.post("/api/dla/entries", input);
}
