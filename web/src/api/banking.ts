import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type BankingAccount = components["schemas"]["BankingAccount"];
export type BankingAccountsResponse =
  components["schemas"]["BankingAccountsResponse"];
export type BankingBatchSummary = components["schemas"]["BankingBatchSummary"];
export type BankingCommandResponse =
  components["schemas"]["BankingCommandResponse"];
export type BankingCreateAccountRequest =
  components["schemas"]["BankingCreateAccountRequest"];
export type BankingMoney = components["schemas"]["BankingMoney"];
export type BankingPayeeRule = components["schemas"]["BankingPayeeRule"];
export type BankingPayeeRuleRequest =
  components["schemas"]["BankingPayeeRuleRequest"];
export type BankingPayeeRulesResponse =
  components["schemas"]["BankingPayeeRulesResponse"];
export type BankingRecentResponse =
  components["schemas"]["BankingRecentResponse"];
export type BankingRecentTransaction =
  components["schemas"]["BankingRecentTransaction"];
export type BankingReceipt = components["schemas"]["BankingReceipt"];
export type BankingReviewCard = components["schemas"]["BankingReviewCard"];
export type BankingReviewQueue = components["schemas"]["BankingReviewQueue"];

export function getBankingAccounts() {
  return apiClient.get("/api/banking/accounts");
}

export function createBankingAccount(account: BankingCreateAccountRequest) {
  return apiClient.post("/api/banking/accounts", account);
}

export function getBankingReviewQueue() {
  return apiClient.get("/api/banking/review");
}

export function getRecentlyReconciled(
  limit = 10,
  accountID: number | null = null,
) {
  return apiClient.get("/api/banking/recent", {
    query: { account: accountID ?? undefined, limit },
  });
}

export function importBankingCSV(accountID: number, file: File) {
  const form = new FormData();
  form.append("file", file, file.name);

  return apiClient.postForm(importPath(accountID), form);
}

export function attachBankingReceipt(transactionID: number, file: File) {
  const form = new FormData();
  form.append("receipt", file, file.name);

  return apiClient.put(transactionReceiptPath(transactionID), form);
}

export function deleteBankingReceipt(transactionID: number) {
  return apiClient.delete(transactionReceiptPath(transactionID));
}

export function getBankingPayeeRules() {
  return apiClient.get("/api/banking/payee-rules");
}

export function createBankingPayeeRule(rule: BankingPayeeRuleRequest) {
  return apiClient.post("/api/banking/payee-rules", rule);
}

export function updateBankingPayeeRule(
  id: number,
  rule: BankingPayeeRuleRequest,
) {
  return apiClient.put(payeeRulePath(id), rule);
}

export function deleteBankingPayeeRule(id: number) {
  return apiClient.delete(payeeRulePath(id));
}

export function confirmBankingMatch(transactionID: number) {
  return apiClient.post(transactionCommandPath(transactionID, "confirm"));
}

export function fileBankingTransactionToDLA(transactionID: number) {
  return apiClient.post(transactionCommandPath(transactionID, "file-dla"));
}

export function recodeBankingTransaction(
  transactionID: number,
  accountCode: string,
) {
  return apiClient.post(transactionCommandPath(transactionID, "recode"), {
    account_code: accountCode,
  });
}

export function unreconcileBankingTransaction(transactionID: number) {
  return apiClient.post(transactionCommandPath(transactionID, "unreconcile"));
}

export function excludeBankingTransaction(
  transactionID: number,
  reason: string,
) {
  return apiClient.post(transactionCommandPath(transactionID, "exclude"), {
    reason,
  });
}

function importPath(accountID: number) {
  return `/api/banking/accounts/${encodeURIComponent(
    String(accountID),
  )}/import` as "/api/banking/accounts/{id}/import";
}

function payeeRulePath(ruleID: number) {
  return `/api/banking/payee-rules/${encodeURIComponent(
    String(ruleID),
  )}` as "/api/banking/payee-rules/{id}";
}

function transactionCommandPath(
  transactionID: number,
  command: "confirm" | "exclude" | "file-dla" | "recode" | "unreconcile",
) {
  return `/api/banking/transactions/${encodeURIComponent(
    String(transactionID),
  )}/${command}` as
    | "/api/banking/transactions/{id}/confirm"
    | "/api/banking/transactions/{id}/exclude"
    | "/api/banking/transactions/{id}/file-dla"
    | "/api/banking/transactions/{id}/recode"
    | "/api/banking/transactions/{id}/unreconcile";
}

function transactionReceiptPath(transactionID: number) {
  return `/api/banking/transactions/${encodeURIComponent(
    String(transactionID),
  )}/receipt` as "/api/banking/transactions/{id}/receipt";
}
