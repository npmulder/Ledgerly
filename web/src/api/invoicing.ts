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
export type InvoicingInvoiceLine =
  components["schemas"]["InvoicingInvoiceLine"];
export type InvoicingInvoiceLineInput =
  components["schemas"]["InvoicingInvoiceLineInput"];
export type InvoicingInvoicePatch =
  components["schemas"]["InvoicingInvoicePatch"];
export type InvoicingInvoicePrintPayload =
  components["schemas"]["InvoicingInvoicePrintPayload"];
export type InvoicingInvoicesResponse =
  components["schemas"]["InvoicingInvoicesResponse"];
export type InvoicingMoneyAmount =
  components["schemas"]["InvoicingMoneyAmount"];
export type InvoicingMoney = components["schemas"]["InvoicingMoney"];
export type InvoicingSendInvoiceResult =
  components["schemas"]["InvoicingSendInvoiceResult"];

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

export function listInvoices() {
  return apiClient.get("/api/invoicing/invoices", {
    query: { limit: 20, offset: 0 },
  });
}

export function createDraftInvoice(
  input: InvoicingCreateDraftInvoiceRequest,
) {
  return apiClient.post("/api/invoicing/invoices", input);
}

export function getInvoice(id: string) {
  return apiClient.get(invoicePath(id));
}

export function patchInvoice(id: string, input: InvoicingInvoicePatch) {
  return apiClient.patch(invoicePath(id), input);
}

export function sendInvoice(id: string) {
  return apiClient.post(invoiceSendPath(id));
}

export function revertInvoice(id: string) {
  return apiClient.post(invoiceRevertPath(id));
}

export function invoicePDFPath(id: string) {
  return `/api/invoicing/invoices/${encodeURIComponent(
    id,
  )}/pdf` as "/api/invoicing/invoices/{id}/pdf";
}

export function getInvoicePrintPayload(id: string, draft = false) {
  return apiClient.get(invoicePrintPath(id), {
    query: draft ? { draft: true } : undefined,
  });
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

function invoicePath(id: string) {
  return `/api/invoicing/invoices/${encodeURIComponent(
    id,
  )}` as "/api/invoicing/invoices/{id}";
}

function invoiceSendPath(id: string) {
  return `/api/invoicing/invoices/${encodeURIComponent(
    id,
  )}/send` as "/api/invoicing/invoices/{id}/send";
}

function invoiceRevertPath(id: string) {
  return `/api/invoicing/invoices/${encodeURIComponent(
    id,
  )}/revert` as "/api/invoicing/invoices/{id}/revert";
}

function invoicePrintPath(id: string) {
  return `/api/invoicing/invoices/${encodeURIComponent(
    id,
  )}/print` as "/api/invoicing/invoices/{id}/print";
}
