import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";

import { isApiError } from "@/api/client";
import {
  cancelRecurringTemplate,
  createDraftInvoice,
  getInvoices,
  getInvoicingClients,
  getRecurringTemplates,
  type InvoicingInvoiceListItem,
  type InvoicingInvoicesResponse,
  type InvoicingInvoiceStatus,
  type InvoicingRecurringTemplate,
  type InvoicingRecurringTemplatesResponse,
} from "@/api/invoicing";
import { queryKeys } from "@/api/queryKeys";
import {
  Badge,
  AdvisorStrip,
  Button,
  Card,
  EmptyState,
  Input,
  PageTitle,
  Pill,
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeaderCell,
  TableRow,
  formatMinorUnits,
} from "@/components";

type InvoiceStatusFilter = InvoicingInvoiceStatus | "all";

const invoiceStatusFilters: readonly {
  label: string;
  value: InvoiceStatusFilter;
}[] = [
  { label: "ALL", value: "all" },
  { label: "DRAFT", value: "draft" },
  { label: "SENT", value: "sent" },
  { label: "PAID", value: "paid" },
  { label: "OVERDUE", value: "overdue" },
];

const dateFormatter = new Intl.DateTimeFormat("en-GB", {
  day: "2-digit",
  month: "short",
  timeZone: "UTC",
});

export function InvoicesScreen() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState<InvoiceStatusFilter>("all");

  const listParams = useMemo(() => ({ search, status }), [search, status]);

  const invoicesQuery = useQuery({
    queryFn: () => getInvoices(listParams),
    queryKey: queryKeys.invoicing.invoices(listParams),
  });
  const clientsQuery = useQuery({
    queryFn: () => getInvoicingClients(false),
    queryKey: queryKeys.invoicing.clients(false),
  });
  const recurringTemplatesQuery = useQuery({
    queryFn: getRecurringTemplates,
    queryKey: queryKeys.invoicing.recurringTemplates(),
  });
  const defaultClient = clientsQuery.data?.clients[0];
  const createDraftMutation = useMutation({
    mutationFn: (clientId: string) =>
      createDraftInvoice({ client_id: clientId }),
    onSuccess: (invoice) => {
      void queryClient.invalidateQueries({
        queryKey: ["invoicing", "invoices"],
      });
      navigate(`/invoices/${encodeURIComponent(invoice.id)}`);
    },
  });
  const cancelRecurringMutation = useMutation({
    mutationFn: (templateId: string) => cancelRecurringTemplate(templateId),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: queryKeys.invoicing.recurringTemplates(),
      });
    },
  });

  const counts = useMemo(
    () => invoiceCounts(invoicesQuery.data),
    [invoicesQuery.data],
  );

  function handleNewInvoice() {
    if (!defaultClient) {
      return;
    }
    createDraftMutation.mutate(defaultClient.id);
  }

  return (
    <div className="invoices-screen">
      <div className="invoices-screen__header">
        <PageTitle>Invoices</PageTitle>
        <Button
          disabled={
            clientsQuery.isPending ||
            createDraftMutation.isPending ||
            !defaultClient
          }
          onClick={handleNewInvoice}
          type="button"
        >
          {createDraftMutation.isPending ? "Creating" : "+ New invoice"}
        </Button>
      </div>

      <AdvisorStrip surface="invoices" />

      <div className="invoices-toolbar">
        <div
          aria-label="Invoice status filters"
          className="invoices-status-filters"
          role="group"
        >
          {invoiceStatusFilters.map((filter) => {
            const selected = filter.value === status;
            const count = countForFilter(counts, filter.value);
            return (
              <button
                aria-label={`${filter.label} ${count}`}
                aria-pressed={selected}
                className="invoices-status-filter"
                key={filter.value}
                onClick={() => setStatus(filter.value)}
                type="button"
              >
                <Pill
                  count={count}
                  variant={
                    selected
                      ? "active"
                      : filter.value === "overdue"
                        ? "danger"
                        : "default"
                  }
                >
                  {filter.label}
                </Pill>
              </button>
            );
          })}
        </div>
        <Input
          aria-label="Search client or number"
          className="invoices-search"
          onChange={(event) => setSearch(event.target.value)}
          placeholder="Search client or number"
          type="search"
          value={search}
        />
      </div>

      {clientsQuery.isError ? (
        <ProblemAlert error={clientsQuery.error} />
      ) : null}
      {createDraftMutation.isError ? (
        <ProblemAlert error={createDraftMutation.error} />
      ) : null}
      {cancelRecurringMutation.isError ? (
        <ProblemAlert error={cancelRecurringMutation.error} />
      ) : null}

      <RecurringTemplatesState
        data={recurringTemplatesQuery.data}
        error={recurringTemplatesQuery.error}
        isError={recurringTemplatesQuery.isError}
        isLoading={recurringTemplatesQuery.isPending}
        onCancel={(templateId) => cancelRecurringMutation.mutate(templateId)}
        pendingCancelId={cancelRecurringMutation.variables}
      />

      <InvoicesTableState
        data={invoicesQuery.data}
        error={invoicesQuery.error}
        isError={invoicesQuery.isError}
        isLoading={invoicesQuery.isPending}
      />
    </div>
  );
}

function RecurringTemplatesState({
  data,
  error,
  isError,
  isLoading,
  onCancel,
  pendingCancelId,
}: {
  readonly data: InvoicingRecurringTemplatesResponse | undefined;
  readonly error: Error | null;
  readonly isError: boolean;
  readonly isLoading: boolean;
  readonly onCancel: (templateId: string) => void;
  readonly pendingCancelId: string | undefined;
}) {
  if (isLoading) {
    return (
      <Card title="Recurring templates">
        <p className="type-secondary">Loading templates.</p>
      </Card>
    );
  }

  if (isError) {
    return <ProblemAlert error={error} />;
  }

  return (
    <RecurringTemplatesTable
      onCancel={onCancel}
      pendingCancelId={pendingCancelId}
      templates={data?.templates ?? []}
    />
  );
}

function RecurringTemplatesTable({
  onCancel,
  pendingCancelId,
  templates,
}: {
  readonly onCancel: (templateId: string) => void;
  readonly pendingCancelId: string | undefined;
  readonly templates: InvoicingRecurringTemplate[];
}) {
  return (
    <Card title="Recurring templates">
      {templates.length === 0 ? (
        <p className="type-secondary">No recurring templates.</p>
      ) : (
        <Table
          aria-label="Recurring templates list"
          className="recurring-templates-table"
        >
          <TableHead>
            <TableRow>
              <TableHeaderCell>Client</TableHeaderCell>
              <TableHeaderCell>Schedule</TableHeaderCell>
              <TableHeaderCell>Next run</TableHeaderCell>
              <TableHeaderCell align="right">Mode</TableHeaderCell>
              <TableHeaderCell align="right">Status</TableHeaderCell>
              <TableHeaderCell align="right">Actions</TableHeaderCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {templates.map((template) => (
              <TableRow key={template.id}>
                <TableCell>{template.client_name}</TableCell>
                <TableCell>{formatSchedule(template)}</TableCell>
                <TableCell>{formatDate(template.next_run_date)}</TableCell>
                <TableCell align="right">
                  {template.auto_send ? "Auto-send" : "Draft"}
                </TableCell>
                <TableCell align="right">
                  <Badge
                    variant={template.status === "active" ? "neutral" : "paid"}
                  >
                    {template.status.toUpperCase()}
                  </Badge>
                </TableCell>
                <TableCell align="right">
                  <Button
                    disabled={
                      template.status !== "active" ||
                      pendingCancelId === template.id
                    }
                    onClick={() => onCancel(template.id)}
                    size="small"
                    variant="secondary"
                  >
                    {pendingCancelId === template.id ? "Canceling" : "Cancel"}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Card>
  );
}

function InvoicesTableState({
  data,
  error,
  isError,
  isLoading,
}: {
  readonly data: InvoicingInvoicesResponse | undefined;
  readonly error: Error | null;
  readonly isError: boolean;
  readonly isLoading: boolean;
}) {
  if (isLoading) {
    return (
      <Card title="Invoices">
        <p className="type-secondary">Loading invoices.</p>
      </Card>
    );
  }

  if (isError) {
    return <ProblemAlert error={error} />;
  }

  if (!data || data.invoices.length === 0) {
    return (
      <EmptyState>
        All caught up — no invoices match the current filters.
      </EmptyState>
    );
  }

  return <InvoicesTable data={data} />;
}

function InvoicesTable({ data }: { readonly data: InvoicingInvoicesResponse }) {
  return (
    <Table aria-label="Invoices list" className="invoices-table">
      <TableHead>
        <TableRow>
          <TableHeaderCell>Number</TableHeaderCell>
          <TableHeaderCell>Client</TableHeaderCell>
          <TableHeaderCell>Issued</TableHeaderCell>
          <TableHeaderCell align="right">Amount</TableHeaderCell>
          <TableHeaderCell align="right">Rate</TableHeaderCell>
          <TableHeaderCell align="right">≈GBP</TableHeaderCell>
          <TableHeaderCell align="right">Status</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {data.invoices.map((invoice) => (
          <TableRow
            key={invoice.id}
            tone={invoice.status === "overdue" ? "overdue" : "default"}
          >
            <TableCell variant="mono">
              <a href={`/invoices/${encodeURIComponent(invoice.id)}`}>
                {invoice.number ?? "DRAFT"}
              </a>
            </TableCell>
            <TableCell>{invoice.client_name}</TableCell>
            <TableCell className="invoices-table__issued">
              {formatDate(invoice.issue_date)}
            </TableCell>
            <TableCell align="right" variant="mono-numeric">
              {formatMoney(invoice.totals.total)}
            </TableCell>
            <TableCell align="right" variant="mono-numeric">
              {formatLockedRate(invoice)}
            </TableCell>
            <TableCell align="right" variant="mono-numeric">
              {formatApproxGBP(invoice)}
            </TableCell>
            <TableCell align="right">
              <InvoiceStatusBadge invoice={invoice} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
      <TableFooter>
        <TableRow>
          <TableCell colSpan={3}>
            Showing {data.invoices.length} of {data.total_count}
          </TableCell>
          <TableCell align="right" colSpan={4}>
            <span className="invoices-table__footer-totals">
              Totals: {formatTotals(data.totals)}
            </span>
          </TableCell>
        </TableRow>
      </TableFooter>
    </Table>
  );
}

function InvoiceStatusBadge({
  invoice,
}: {
  readonly invoice: InvoicingInvoiceListItem;
}) {
  if (invoice.status === "overdue") {
    return <Badge daysOverdue={invoice.days_overdue} variant="overdue" />;
  }
  return <Badge variant={invoice.status} />;
}

function ProblemAlert({ error }: { readonly error: Error | null }) {
  if (isApiError(error)) {
    return (
      <div className="problem-alert" role="alert">
        <strong>{error.problem.title}</strong>
        {error.problem.detail ? <span>{error.problem.detail}</span> : null}
      </div>
    );
  }

  return (
    <div className="problem-alert" role="alert">
      <strong>Request failed</strong>
      {error?.message ? <span>{error.message}</span> : null}
    </div>
  );
}

function invoiceCounts(data: InvoicingInvoicesResponse | undefined) {
  const counts: Record<InvoicingInvoiceStatus, number> = {
    draft: 0,
    overdue: 0,
    paid: 0,
    sent: 0,
  };
  for (const item of data?.counts ?? []) {
    counts[item.status] = item.count;
  }
  return counts;
}

function countForFilter(
  counts: Record<InvoicingInvoiceStatus, number>,
  filter: InvoiceStatusFilter,
) {
  if (filter === "all") {
    return Object.values(counts).reduce((sum, count) => sum + count, 0);
  }
  return counts[filter];
}

function formatDate(value: string) {
  return dateFormatter.format(new Date(value));
}

function formatSchedule(template: InvoicingRecurringTemplate) {
  const cadence = template.cadence === "quarterly" ? "Quarterly" : "Monthly";
  return `${cadence} on day ${template.day_of_month}`;
}

function formatMoney({
  amount,
  currency,
}: {
  readonly amount: number;
  readonly currency: string;
}) {
  return formatMinorUnits({ amountMinor: amount, currency });
}

function formatLockedRate(invoice: InvoicingInvoiceListItem) {
  if (invoice.status === "draft" || invoice.currency === "GBP") {
    return "—";
  }

  return invoice.totals.approx_gbp?.rate.value ?? "—";
}

function formatApproxGBP(invoice: InvoicingInvoiceListItem) {
  const total = invoice.totals.total;
  if (total.currency === "GBP") {
    return formatMoney(total);
  }

  const approx = invoice.totals.approx_gbp?.amount;
  return approx ? formatMoney(approx) : "—";
}

function formatTotals(totals: InvoicingInvoicesResponse["totals"]) {
  const nativeTotals = totals.subtotals.map(formatMoney).join(" + ") || "—";
  return `${nativeTotals} ≈ ${formatMoney(totals.total_gbp)}`;
}
