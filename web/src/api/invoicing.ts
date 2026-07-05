import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type InvoicingClient = components["schemas"]["InvoicingClient"];
export type InvoicingClientPatch =
  components["schemas"]["InvoicingClientPatch"];
export type InvoicingClientRequest =
  components["schemas"]["InvoicingClientRequest"];
export type InvoicingMoneyAmount =
  components["schemas"]["InvoicingMoneyAmount"];

export function getInvoicingClients(includeArchived = false) {
  return apiClient.get("/api/invoicing/clients", {
    query: includeArchived ? { include_archived: true } : undefined,
  });
}

export function createInvoicingClient(input: InvoicingClientRequest) {
  return apiClient.post("/api/invoicing/clients", input);
}

export function patchInvoicingClient(
  id: string,
  input: InvoicingClientPatch,
) {
  return apiClient.patch(clientPath(id), input);
}

export function archiveInvoicingClient(id: string) {
  return apiClient.post(clientArchivePath(id));
}

function clientPath(id: string) {
  return `/api/invoicing/clients/${encodeURIComponent(
    id,
  )}` as "/api/invoicing/clients/{id}";
}

function clientArchivePath(id: string) {
  return `/api/invoicing/clients/${encodeURIComponent(
    id,
  )}/archive` as "/api/invoicing/clients/{id}/archive";
}
