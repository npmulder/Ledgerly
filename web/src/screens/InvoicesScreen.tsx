import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";

import { isApiError } from "@/api/client";
import {
  createDraftInvoice,
  getInvoicingClients,
  listInvoices,
  resolveInvoicingCTA,
} from "@/api/invoicing";
import { queryKeys } from "@/api/queryKeys";
import {
  Badge,
  Button,
  Card,
  EmptyState,
  Field,
  PageTitle,
  Select,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  formatMinorUnits,
} from "@/components";

export function InvoicesScreen() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [clientId, setClientId] = useState("");
  const [reminderToast, setReminderToast] = useState("");
  const [reminderError, setReminderError] = useState("");
  const clientsQuery = useQuery({
    queryFn: () => getInvoicingClients(false),
    queryKey: queryKeys.invoicing.clients(false),
  });
  const invoicesQuery = useQuery({
    queryFn: listInvoices,
    queryKey: queryKeys.invoicing.invoices(),
  });
  const clients = clientsQuery.data?.clients ?? [];
  const selectedClientId = clientId || clients[0]?.id || "";
  const overdueInvoice = invoicesQuery.data?.invoices.find(
    (invoice) => invoice.status === "overdue",
  );

  const createMutation = useMutation({
    mutationFn: () => createDraftInvoice({ client_id: selectedClientId }),
    onSuccess: (invoice) => {
      queryClient.setQueryData(queryKeys.invoicing.invoice(invoice.id), invoice);
      queryClient.invalidateQueries({ queryKey: queryKeys.invoicing.invoices() });
      navigate(`/invoices/${encodeURIComponent(invoice.id)}`);
    },
  });

  const reminderMutation = useMutation({
    mutationFn: (invoiceID: string) =>
      resolveInvoicingCTA("invoicing.sendReminder", { invoice_id: invoiceID }),
    onError: (error) => {
      setReminderToast("");
      setReminderError(problemMessage(error, "Unable to send reminder"));
    },
    onSuccess: (result) => {
      const number = result.invoice.number ?? "invoice";
      setReminderError("");
      setReminderToast(`Reminder sent for ${number}.`);
      queryClient.setQueryData(
        queryKeys.invoicing.invoice(result.invoice.id),
        result.invoice,
      );
      queryClient.invalidateQueries({ queryKey: queryKeys.invoicing.invoices() });
    },
  });

  return (
    <div className="invoices-screen">
      <PageTitle>Invoices</PageTitle>
      <div className="invoices-screen__grid">
        <Card
          actions={
            <Button
              disabled={!selectedClientId || createMutation.isPending}
              onClick={() => createMutation.mutate()}
            >
              {createMutation.isPending ? "Creating" : "Create draft"}
            </Button>
          }
          title="New invoice"
        >
          <Field helperText="Archived clients are excluded." label="Client">
            <Select
              disabled={clientsQuery.isPending || clients.length === 0}
              onChange={(event) => setClientId(event.target.value)}
              value={selectedClientId}
            >
              {clients.map((client) => (
                <option key={client.id} value={client.id}>
                  {client.name}
                </option>
              ))}
            </Select>
          </Field>
          {clientsQuery.isError && (
            <p className="problem-alert" role="alert">
              Unable to load clients.
            </p>
          )}
        </Card>

        <Card title="Recent invoices">
          {overdueInvoice && (
            <div className="invoice-advisor-strip" role="region" aria-label="Invoice advisor">
              <span>
                <strong>Advisor:</strong> {overdueInvoice.client_name} is{" "}
                {overdueInvoice.days_overdue} days overdue on{" "}
                {formatMinorUnits({
                  amountMinor: overdueInvoice.totals.total.amount,
                  currency: overdueInvoice.totals.total.currency,
                })}
                .
              </span>
              <Button
                disabled={reminderMutation.isPending}
                onClick={() => reminderMutation.mutate(overdueInvoice.id)}
                size="small"
              >
                {reminderMutation.isPending ? "Sending" : "Send reminder"}
              </Button>
            </div>
          )}
          {reminderToast && (
            <div className="invoice-toast" role="status">
              {reminderToast}
            </div>
          )}
          {reminderError && (
            <p className="problem-alert" role="alert">
              {reminderError}
            </p>
          )}
          {invoicesQuery.isPending && (
            <p className="type-secondary">Loading invoices.</p>
          )}
          {invoicesQuery.isError && (
            <p className="problem-alert" role="alert">
              Unable to load invoices.
            </p>
          )}
          {invoicesQuery.data && invoicesQuery.data.invoices.length === 0 && (
            <EmptyState icon="+" title="No invoices">
              Create a draft invoice to start billing a client.
            </EmptyState>
          )}
          {invoicesQuery.data && invoicesQuery.data.invoices.length > 0 && (
            <Table aria-label="Recent invoices">
              <TableHead>
                <TableRow>
                  <TableHeaderCell>Number</TableHeaderCell>
                  <TableHeaderCell>Client</TableHeaderCell>
                  <TableHeaderCell>Due</TableHeaderCell>
                  <TableHeaderCell align="right">Total</TableHeaderCell>
                  <TableHeaderCell>Status</TableHeaderCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {invoicesQuery.data.invoices.map((invoice) => (
                  <TableRow key={invoice.id}>
                    <TableCell>
                      <a href={`/invoices/${encodeURIComponent(invoice.id)}`}>
                        {invoice.number ?? "Draft"}
                      </a>
                    </TableCell>
                    <TableCell>{invoice.client_name}</TableCell>
                    <TableCell>{invoice.due_date.slice(0, 10)}</TableCell>
                    <TableCell align="right" variant="mono-numeric">
                      {formatMinorUnits({
                        amountMinor: invoice.totals.total.amount,
                        currency: invoice.totals.total.currency,
                      })}
                    </TableCell>
                    <TableCell>
                      <Badge variant={invoice.status}>
                        {invoice.status === "overdue" ? "sent" : invoice.status}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Card>
      </div>
    </div>
  );
}

function problemMessage(error: unknown, fallbackTitle: string) {
  if (isApiError(error)) {
    return error.problem.detail
      ? `${error.problem.title}: ${error.problem.detail}`
      : error.problem.title;
  }
  return fallbackTitle;
}
