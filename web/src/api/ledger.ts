import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type LedgerAccount = components["schemas"]["LedgerAccount"];
export type LedgerAccountsResponse =
  components["schemas"]["LedgerAccountsResponse"];
export type LedgerCreateExpenseAccountRequest =
  components["schemas"]["LedgerCreateExpenseAccountRequest"];

export function getExpenseAccounts() {
  return apiClient.get("/api/ledger/accounts", {
    query: { type: "expense" },
  });
}

export function createExpenseAccount(
  input: Pick<LedgerCreateExpenseAccountRequest, "code" | "name">,
) {
  return apiClient.post("/api/ledger/accounts", {
    ...input,
    type: "expense",
  });
}
