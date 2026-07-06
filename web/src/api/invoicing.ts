import { apiClient } from "@/api/client";
import type { components } from "@/api/generated/schema";

export type InvoicingClient = components["schemas"]["InvoicingClient"];
export type InvoicingClientPatch =
  components["schemas"]["InvoicingClientPatch"];
export type InvoicingClientRequest =
  components["schemas"]["InvoicingClientRequest"];
export type InvoicingCreateDraftInvoiceRequest =
  components["schemas"]["InvoicingCreateDraftInvoiceRequest"];
export type InvoicingInvoice = components["schemas"]["InvoicingInvoice"];
export type InvoicingInvoiceListItem =
  components["schemas"]["InvoicingInvoiceListItem"];
export type InvoicingInvoiceStatus = InvoicingInvoiceListItem["status"];
export type InvoicingInvoicesResponse =
  components["schemas"]["InvoicingInvoicesResponse"];
export type InvoicingMoneyAmount =
  components["schemas"]["InvoicingMoneyAmount"];

export type InvoicesListParams = {
  readonly limit?: number;
  readonly offset?: number;
  readonly search?: string;
  readonly status?: InvoicingInvoiceStatus | "all";
};

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

export function getInvoices({
  limit = 50,
  offset = 0,
  search,
  status = "all",
}: InvoicesListParams = {}) {
  const trimmedSearch = search?.trim();
  return apiClient.get("/api/invoicing/invoices", {
    query: {
      limit,
      offset,
      search: trimmedSearch || undefined,
      status: status === "all" ? undefined : [status],
    },
  });
}

export function createDraftInvoice(input: InvoicingCreateDraftInvoiceRequest) {
  return apiClient.post("/api/invoicing/invoices", input);
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
