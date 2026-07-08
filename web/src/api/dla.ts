import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type DLABalance = components["schemas"]["DLABalanceResponse"];
export type DLAStatuses = components["schemas"]["DLAStatusesResponse"];
export type DLAEntryRequest = components["schemas"]["DLAEntryRequest"];
export type DLAEntry = components["schemas"]["DLALedgerEntry"];
export type DLALedger = components["schemas"]["DLALedgerResponse"];
export type DLAMoney = components["schemas"]["DLAMoney"];

export function getDLABalance(directorID = "director-1") {
  return apiClient.get("/api/dla/balance", {
    query: { director: directorID },
  });
}

export function getDLAStatuses() {
  return apiClient.get("/api/dla/statuses");
}

export function getDLALedger(directorID = "director-1", cursor?: string | null) {
  return apiClient.get("/api/dla/ledger", {
    query: { cursor: cursor ?? undefined, director: directorID },
  });
}

export function createDLAEntry(input: DLAEntryRequest) {
  return apiClient.post("/api/dla/entries", input);
}
