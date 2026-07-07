import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type DividendsDocumentPayload =
  components["schemas"]["DividendsDocumentPayload"];
export type DividendsDeclaration =
  components["schemas"]["DividendsDeclaration"];
export type DividendsHeadroom =
  components["schemas"]["DividendsHeadroomBreakdown"];
export type DividendsHistoryResponse =
  components["schemas"]["DividendsHistoryResponse"];
export type DividendsMoney = components["schemas"]["DividendsMoney"];
export type DividendsValidationResult =
  components["schemas"]["DividendsValidationResult"];

export function getDividendHeadroom() {
  return apiClient.get("/api/dividends/headroom");
}

export function validateDividendAmount(amount: DividendsMoney) {
  return apiClient.post("/api/dividends/validate", { amount });
}

export function declareDividendAmount(amount: DividendsMoney) {
  return apiClient.post("/api/dividends/declare", { amount });
}

export function getDividendHistory() {
  return apiClient.get("/api/dividends/history");
}

export function getDividendDocumentPayload(id: string) {
  return apiClient.get(dividendDocumentPayloadPath(id));
}

export function renderDividendDocuments(id: string) {
  return apiClient.post(dividendDocumentRenderPath(id));
}

export function dividendVoucherPDFPath(id: string) {
  return `/api/dividends/${encodeURIComponent(
    id,
  )}/voucher` as "/api/dividends/{id}/voucher";
}

export function boardMinutesPDFPath(id: string) {
  return `/api/dividends/${encodeURIComponent(
    id,
  )}/minutes` as "/api/dividends/{id}/minutes";
}

function dividendDocumentPayloadPath(id: string) {
  return `/api/dividends/declarations/${encodeURIComponent(
    id,
  )}/print` as "/api/dividends/declarations/{id}/print";
}

function dividendDocumentRenderPath(id: string) {
  return `/api/dividends/declarations/${encodeURIComponent(
    id,
  )}/documents/render` as "/api/dividends/declarations/{id}/documents/render";
}
