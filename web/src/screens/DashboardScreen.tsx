import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";

import { isApiError } from "@/api/client";
import {
  getDashboardSummary,
  type DashboardCash,
  type DashboardDLA,
  type DashboardDividendHeadroom,
  type DashboardMoney,
  type DashboardOutstanding,
  type DashboardRate,
  type DashboardRecentInvoice,
  type DashboardReviewQueueItem,
  type DashboardToReconcile,
} from "@/api/dashboard";
import {
  createDraftInvoice,
  getInvoicingClients,
  patchInvoice,
  type InvoicingClient,
  type InvoicingMoneyAmount,
} from "@/api/invoicing";
import { queryKeys } from "@/api/queryKeys";
import type { BadgeVariant } from "@/components";
import {
  Badge,
  Button,
  Card,
  EmptyState,
  PageTitle,
  Panel,
  StatCard,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  formatMinorUnits,
} from "@/components";

export function DashboardScreen() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const today = useToday();
  const currentMonth = monthName(today);

  const summaryQuery = useQuery({
    queryFn: getDashboardSummary,
    queryKey: queryKeys.dashboard.summary(),
  });
  const clientsQuery = useQuery({
    queryFn: () => getInvoicingClients(false),
    queryKey: queryKeys.invoicing.clients(false),
  });

  const retainerClient = useMemo(
    () => clientsQuery.data?.clients.find(hasRetainerAmount) ?? null,
    [clientsQuery.data?.clients],
  );
  const ctaLabel = retainerClient
    ? `Raise ${currentMonth} invoice`
    : "New invoice";

  const raiseRetainerMutation = useMutation({
    mutationFn: async () => {
      if (!retainerClient?.retainer_amount) {
        throw new Error("No retainer client is available");
      }
      const invoiceDate = new Date();
      const issueDate = dateInputValue(invoiceDate);
      const invoiceMonth = monthName(invoiceDate);
      const draft = await createDraftInvoice({ client_id: retainerClient.id });
      return patchInvoice(draft.id, {
        client_id: retainerClient.id,
        currency: retainerClient.default_currency,
        due_date: addDays(issueDate, retainerClient.terms_days),
        issue_date: issueDate,
        lines: [
          {
            description: `${invoiceMonth} retainer`,
            id: retainerLineId(issueDate),
            qty: "1",
            unit_price: {
              amount: retainerClient.retainer_amount.amount_minor,
              currency: retainerClient.retainer_amount.currency,
            },
          },
        ],
        vat_treatment: retainerClient.vat_treatment,
      });
    },
    onSuccess: (invoice) => {
      queryClient.setQueryData(queryKeys.invoicing.invoice(invoice.id), invoice);
      queryClient.invalidateQueries({ queryKey: queryKeys.invoicing.invoices() });
      navigate(`/invoices/${encodeURIComponent(invoice.id)}`);
    },
  });

  function handlePrimaryCTA() {
    if (retainerClient) {
      raiseRetainerMutation.mutate();
      return;
    }
    navigate("/invoices");
  }

  const summary = summaryQuery.data;
  const greetingName = greetingFirstName(summary?.greeting?.user_name ?? "");
  const tradingName = summary?.greeting?.trading_name ?? "Ledgerly";

  return (
    <div className="dashboard-screen">
      <PageTitle>Dashboard</PageTitle>

      <div className="dashboard-greeting">
        <div className="dashboard-greeting__copy">
          <p className="type-secondary">
            {greetingName ? `Good morning, ${greetingName}` : "Good morning"}
          </p>
          <p className="dashboard-greeting__company">{tradingName}</p>
        </div>
        <div className="dashboard-greeting__actions">
          <Button
            disabled={clientsQuery.isPending || raiseRetainerMutation.isPending}
            onClick={handlePrimaryCTA}
          >
            {raiseRetainerMutation.isPending ? "Creating draft" : ctaLabel}
          </Button>
          {retainerClient ? (
            <p className="dashboard-greeting__retainer">
              Retainer: {retainerClient.name}
            </p>
          ) : null}
        </div>
      </div>

      {summaryQuery.isPending ? (
        <p className="type-secondary">Loading dashboard.</p>
      ) : null}
      {summaryQuery.isError ? (
        <ProblemAlert
          error={summaryQuery.error}
          fallbackTitle="Unable to load dashboard."
        />
      ) : null}
      {raiseRetainerMutation.isError ? (
        <ProblemAlert
          error={raiseRetainerMutation.error}
          fallbackTitle="Unable to create retainer invoice."
        />
      ) : null}

      {summary ? (
        <>
          <DashboardStatus errors={summary.errors} />
          <section className="dashboard-stat-grid" aria-label="Dashboard stats">
            <CashStat cash={summary.cash} />
            <OutstandingStat outstanding={summary.outstanding} />
            <DLAStat dla={summary.dla} />
            <DividendHeadroomStat headroom={summary.dividendHeadroom} />
          </section>

          <section className="dashboard-content-grid">
            <div className="dashboard-content-stack">
              <RecentInvoices invoices={summary.recentInvoices} />
              <ToReconcilePreview toReconcile={summary.toReconcile} />
            </div>
            <aside className="dashboard-side-stack" aria-label="Dashboard side panel">
              <AdvisorPlaceholder />
              <RateCard rate={summary.rate} today={today} />
            </aside>
          </section>
        </>
      ) : null}
    </div>
  );
}

function DashboardStatus({
  errors,
}: {
  readonly errors: ReadonlyArray<{ detail: string; section: string }>;
}) {
  if (errors.length === 0) {
    return null;
  }
  return (
    <div className="dashboard-section-note" role="status">
      Some dashboard sections are unavailable.
    </div>
  );
}

function CashStat({ cash }: { readonly cash: DashboardCash | null }) {
  if (!cash) {
    return <UnavailableStat label="Cash" />;
  }
  return (
    <StatCard
      label="Cash (GBP equiv.)"
      secondary={cashBreakdown(cash)}
      value={formatMoney(cash.total_gbp)}
    />
  );
}

function OutstandingStat({
  outstanding,
}: {
  readonly outstanding: DashboardOutstanding | null;
}) {
  if (!outstanding) {
    return <UnavailableStat label="Outstanding" />;
  }
  const primary = outstanding.totals[0] ?? outstanding.total_gbp;
  const dueDate = outstanding.earliest_due_date
    ? `due ${formatShortDate(outstanding.earliest_due_date)}`
    : "no due date";

  return (
    <StatCard
      className="dashboard-stat-card--teal"
      label="Outstanding"
      secondary={`≈GBP ${formatMoney(outstanding.total_gbp)} · ${dueDate}`}
      value={formatMoney(primary)}
    />
  );
}

function DLAStat({ dla }: { readonly dla: DashboardDLA | null }) {
  if (!dla) {
    return <UnavailableStat label="Director's loan" />;
  }
  const isOverdrawn = dla.status === "overdrawn";
  return (
    <StatCard
      className={
        isOverdrawn
          ? "dashboard-stat-card--amber"
          : "dashboard-stat-card--teal"
      }
      label="Director's loan"
      secondary={isOverdrawn ? "Overdrawn" : "Company owes you"}
      value={`${formatMoney(dla.balance, { absolute: true })} ${
        isOverdrawn ? "DR" : "CR"
      }`}
    />
  );
}

function DividendHeadroomStat({
  headroom,
}: {
  readonly headroom: DashboardDividendHeadroom | null;
}) {
  if (!headroom) {
    return <UnavailableStat label="Dividend headroom" />;
  }
  return (
    <StatCard
      className={
        headroom.distributable
          ? "dashboard-stat-card--navy"
          : "dashboard-stat-card--muted"
      }
      label="Dividend headroom"
      secondary={
        headroom.distributable
          ? "0% CIT · reserves distributable"
          : "Not currently distributable"
      }
      value={formatMoney(headroom.available)}
    />
  );
}

function UnavailableStat({ label }: { readonly label: string }) {
  return (
    <StatCard
      className="dashboard-stat-card--unavailable"
      label={label}
      secondary="Section unavailable"
      value="Unavailable"
    />
  );
}

function RecentInvoices({
  invoices,
}: {
  readonly invoices: DashboardRecentInvoice[] | null;
}) {
  return (
    <Card
      actions={
        <Link className="dashboard-card-link" to="/invoices">
          View all
        </Link>
      }
      title="Recent invoices"
    >
      {!invoices ? (
        <QuietUnavailable title="Recent invoices unavailable" />
      ) : invoices.length === 0 ? (
        <EmptyState icon="#" title="No recent invoices">
          Invoice activity will appear here.
        </EmptyState>
      ) : (
        <Table aria-label="Recent invoices">
          <TableHead>
            <TableRow>
              <TableHeaderCell>Number</TableHeaderCell>
              <TableHeaderCell>Client</TableHeaderCell>
              <TableHeaderCell align="right">Amount</TableHeaderCell>
              <TableHeaderCell>Status</TableHeaderCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {invoices.slice(0, 5).map((invoice) => (
              <TableRow
                key={invoice.id}
                tone={invoice.status === "overdue" ? "overdue" : "default"}
              >
                <TableCell variant="mono">
                  <Link to={`/invoices/${encodeURIComponent(invoice.id)}`}>
                    {invoice.number ?? "Draft"}
                  </Link>
                </TableCell>
                <TableCell>{invoice.client}</TableCell>
                <TableCell align="right" variant="mono-numeric">
                  {formatMoney(invoice.amount)}
                </TableCell>
                <TableCell>
                  <Badge
                    daysOverdue={invoice.days_overdue ?? undefined}
                    variant={invoiceBadgeVariant(invoice.status)}
                  />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Card>
  );
}

function ToReconcilePreview({
  toReconcile,
}: {
  readonly toReconcile: DashboardToReconcile | null;
}) {
  const totalCount =
    toReconcile?.accounts.reduce(
      (sum, account) => sum + account.unreconciled_count,
      0,
    ) ?? 0;

  return (
    <Card
      actions={
        <Link className="dashboard-card-link" to="/banking">
          Open banking
        </Link>
      }
      title="To reconcile"
    >
      {!toReconcile ? (
        <QuietUnavailable title="Reconciliation unavailable" />
      ) : (
        <div className="dashboard-reconcile">
          <Badge variant="count">{totalCount}</Badge>
          <div className="dashboard-account-list" aria-label="Accounts">
            {toReconcile.accounts.map((account) => (
              <Link
                className="dashboard-account-row"
                key={account.id}
                to="/banking"
              >
                <span>
                  <strong>{account.name}</strong>
                  <span>{account.currency}</span>
                </span>
                <Badge variant="count">{account.unreconciled_count}</Badge>
              </Link>
            ))}
          </div>
          {toReconcile.review_queue.length === 0 ? (
            <p className="type-secondary">No review queue items.</p>
          ) : (
            <div className="dashboard-review-list" aria-label="Review queue">
              {toReconcile.review_queue.slice(0, 3).map((item, index) => (
                <ReviewQueueLink item={item} key={`${item.kind}-${index}`} />
              ))}
            </div>
          )}
        </div>
      )}
    </Card>
  );
}

function ReviewQueueLink({
  item,
}: {
  readonly item: DashboardReviewQueueItem;
}) {
  return (
    <Link className="dashboard-review-row" to="/banking">
      <span>
        <strong>{item.payee}</strong>
        <span>{reviewKindLabel(item.kind)}</span>
      </span>
      <span className="dashboard-review-row__amount">
        {formatMoney(item.amount)}
      </span>
    </Link>
  );
}

function AdvisorPlaceholder() {
  return (
    <Panel
      aria-label="Advisor panel placeholder"
      className="dashboard-advisor-placeholder"
      eyebrow="Advisor"
      title="Isle of Man rules"
      variant="advisor"
    >
      <div aria-hidden="true" className="dashboard-advisor-placeholder__bar" />
      <div aria-hidden="true" className="dashboard-advisor-placeholder__bar" />
      <div aria-hidden="true" className="dashboard-advisor-placeholder__bar" />
      <div aria-hidden="true" className="dashboard-advisor-placeholder__bar" />
    </Panel>
  );
}

function RateCard({
  rate,
  today,
}: {
  readonly rate: DashboardRate | null;
  readonly today: Date;
}) {
  if (!rate) {
    return (
      <Card title="EUR / GBP · ECB daily">
        <QuietUnavailable title="Rate unavailable" />
      </Card>
    );
  }
  const stale = rate.rate_date < dateInputValue(today);
  return (
    <Card
      className={stale ? "dashboard-rate-card--stale" : undefined}
      title={`${rate.from} / ${rate.to} · ${rate.source}`}
    >
      <div className="dashboard-rate">
        <div className="dashboard-rate__header">
          <span className="dashboard-rate__value">{rate.rate}</span>
          {stale ? <Badge variant="overdue">STALE</Badge> : null}
        </div>
        <p>Frozen onto today's postings</p>
        <p className="type-secondary">
          Rate {formatLongDate(rate.rate_date)} · fetched{" "}
          {formatDateTime(rate.fetched_at)}
        </p>
      </div>
    </Card>
  );
}

function QuietUnavailable({ title }: { readonly title: string }) {
  return (
    <div className="dashboard-unavailable" role="status">
      <strong>{title}</strong>
      <span>Section unavailable.</span>
    </div>
  );
}

function useToday() {
  const [today, setToday] = useState(() => new Date());

  useEffect(() => {
    let timerID: number | undefined;

    function scheduleMidnightRefresh() {
      const now = new Date();
      const nextUTCDate = new Date(
        Date.UTC(
          now.getUTCFullYear(),
          now.getUTCMonth(),
          now.getUTCDate() + 1,
        ),
      );
      timerID = window.setTimeout(() => {
        setToday(new Date());
        scheduleMidnightRefresh();
      }, nextUTCDate.getTime() - now.getTime() + 1000);
    }

    scheduleMidnightRefresh();
    return () => {
      if (timerID !== undefined) {
        window.clearTimeout(timerID);
      }
    };
  }, []);

  return today;
}

function ProblemAlert({
  error,
  fallbackTitle,
}: {
  readonly error: unknown;
  readonly fallbackTitle: string;
}) {
  return (
    <div className="problem-alert" role="alert">
      <strong>{problemMessage(error, fallbackTitle)}</strong>
      {isApiError(error) && error.problem.detail ? (
        <span>{error.problem.detail}</span>
      ) : null}
    </div>
  );
}

function hasRetainerAmount(
  client: InvoicingClient,
): client is InvoicingClient & { retainer_amount: InvoicingMoneyAmount } {
  return client.retainer_amount !== null;
}

function cashBreakdown(cash: DashboardCash) {
  const byCurrency = new Map<string, number>();
  for (const account of cash.accounts) {
    byCurrency.set(
      account.native_balance.currency,
      (byCurrency.get(account.native_balance.currency) ?? 0) +
        account.native_balance.amount,
    );
  }
  if (byCurrency.size === 0) {
    return "No connected balances";
  }
  return [...byCurrency.entries()]
    .map(([currency, amount]) => formatMoney({ amount, currency }))
    .join(" · ");
}

function invoiceBadgeVariant(status: string): BadgeVariant {
  switch (status) {
    case "draft":
    case "overdue":
    case "paid":
    case "sent":
      return status;
    default:
      return "neutral";
  }
}

function reviewKindLabel(kind: string) {
  switch (kind) {
    case "invoice-match":
      return "Invoice match";
    case "dla":
      return "DLA suggestion";
    case "payee-rule":
      return "Payee rule";
    default:
      return "Review item";
  }
}

function greetingFirstName(name: string) {
  return name.trim().split(/\s+/)[0] ?? "";
}

function monthName(date: Date) {
  return new Intl.DateTimeFormat("en-GB", {
    month: "long",
    timeZone: "UTC",
  }).format(date);
}

function dateInputValue(date: Date) {
  const year = date.getUTCFullYear();
  const month = String(date.getUTCMonth() + 1).padStart(2, "0");
  const day = String(date.getUTCDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function addDays(date: string, days: number) {
  const parsed = new Date(`${date}T00:00:00Z`);
  parsed.setUTCDate(parsed.getUTCDate() + days);
  return dateInputValue(parsed);
}

function retainerLineId(issueDate: string) {
  return `line_retainer_${issueDate.slice(0, 7).replace("-", "_")}`;
}

function formatMoney(
  money: DashboardMoney | InvoicingMoneyAmount,
  options: { readonly absolute?: boolean } = {},
) {
  const amountMinor = "amount" in money ? money.amount : money.amount_minor;
  return formatMinorUnits({
    amountMinor: options.absolute ? Math.abs(amountMinor) : amountMinor,
    currency: money.currency,
  });
}

function formatShortDate(date: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "numeric",
    month: "short",
    timeZone: "UTC",
  }).format(new Date(`${date}T00:00:00Z`));
}

function formatLongDate(date: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "numeric",
    month: "short",
    timeZone: "UTC",
    year: "numeric",
  }).format(new Date(`${date}T00:00:00Z`));
}

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    month: "short",
    timeZone: "UTC",
    year: "numeric",
  }).format(new Date(value));
}

function problemMessage(error: unknown, fallbackTitle: string) {
  if (isApiError(error)) {
    return error.problem.title;
  }
  return error instanceof Error ? error.message : fallbackTitle;
}
