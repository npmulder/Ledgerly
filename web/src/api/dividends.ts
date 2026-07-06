import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type DividendsDocumentPayload =
  components["schemas"]["DividendsDocumentPayload"];
export type DividendsDeclaration =
  components["schemas"]["DividendsDeclaration"];

export function getDividendDocumentPayload(id: string) {
  return apiClient.get(dividendDocumentPayloadPath(id));
}

export function renderDividendDocuments(id: string) {
  return apiClient.post(dividendDocumentRenderPath(id));
}

export function dividendVoucherPDFPath(id: string) {
  return `/api/dividends/declarations/${encodeURIComponent(
    id,
  )}/voucher` as "/api/dividends/declarations/{id}/voucher";
}

export function boardMinutesPDFPath(id: string) {
  return `/api/dividends/declarations/${encodeURIComponent(
    id,
  )}/minutes` as "/api/dividends/declarations/{id}/minutes";
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
