import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type AuditEntry = components["schemas"]["AuditEntry"];
export type AuditHistoryResponse =
  components["schemas"]["AuditHistoryResponse"];

export type AuditEntityRef = {
  readonly entity: string;
  readonly entityId: string;
  readonly module: string;
};

export function getAuditHistory(ref: AuditEntityRef) {
  return apiClient.get(auditHistoryPath(ref), {
    query: { limit: 50 },
  });
}

function auditHistoryPath(ref: AuditEntityRef) {
  return `/api/audit/history/${encodeURIComponent(
    ref.module,
  )}/${encodeURIComponent(ref.entity)}/${encodeURIComponent(
    ref.entityId,
  )}` as "/api/audit/history/{module}/{entity}/{entity_id}";
}
