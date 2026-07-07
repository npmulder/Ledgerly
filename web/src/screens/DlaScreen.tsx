import { type FormEvent, useMemo, useRef, useState } from "react";
import {
  type InfiniteData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";

import { isApiError } from "@/api/client";
import {
  createDLAEntry,
  getDLABalance,
  getDLALedger,
  type DLABalance,
  type DLAEntry,
  type DLAEntryRequest,
  type DLALedger,
  type DLAMoney,
} from "@/api/dla";
import { getIdentityProfile } from "@/api/identity";
import { queryKeys } from "@/api/queryKeys";
import {
  AdvisorStrip,
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  PageTitle,
  Select,
  SplitMain,
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeaderCell,
  TableRow,
  formatMinorUnits,
} from "@/components";

const repaymentCashAccount = "1000-cash-gbp";

const expenseCategories = [
  { label: "Fees", value: "5000-fees" },
  { label: "Software", value: "5010-software" },
  { label: "Travel", value: "5020-travel" },
  { label: "Office", value: "5030-office" },
] as const;

type ManualKind = "repayment" | "expense-owed";

type EntryFormState = {
  amount: string;
  category: string;
  date: string;
  description: string;
  kind: ManualKind;
};

type CreateDLAEntryResult = Awaited<ReturnType<typeof createDLAEntry>>;

type CreateEntryContext = {
  previousBalance?: DLABalance;
  previousLedger?: InfiniteData<DLALedger>;
};

const initialFormState = (): EntryFormState => ({
  amount: "",
  category: expenseCategories[1].value,
  date: new Date().toISOString().slice(0, 10),
  description: "",
  kind: "repayment",
});

export function DlaScreen() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const amountRef = useRef<HTMLInputElement>(null);
  const [form, setForm] = useState<EntryFormState>(() => initialFormState());

  const profileQuery = useQuery({
    queryFn: getIdentityProfile,
    queryKey: queryKeys.identity.profile(),
  });
  const balanceQuery = useQuery({
    queryFn: getDLABalance,
    queryKey: queryKeys.dla.balance(),
  });
  const ledgerQuery = useInfiniteQuery<
    DLALedger,
    Error,
    InfiniteData<DLALedger>,
    ReturnType<typeof queryKeys.dla.ledgerPages>,
    string | null
  >({
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    initialPageParam: null as string | null,
    queryFn: ({ pageParam }) => getDLALedger(pageParam),
    queryKey: queryKeys.dla.ledgerPages(),
  });

  const createEntryMutation = useMutation<
    CreateDLAEntryResult,
    Error,
    DLAEntryRequest,
    CreateEntryContext
  >({
    mutationFn: createDLAEntry,
    onError: (_error, _entry, context) => {
      if (context?.previousBalance) {
        queryClient.setQueryData(
          queryKeys.dla.balance(),
          context.previousBalance,
        );
      }
      if (context?.previousLedger) {
        queryClient.setQueryData(
          queryKeys.dla.ledgerPages(),
          context.previousLedger,
        );
      }
    },
    onMutate: async (entry) => {
      await queryClient.cancelQueries({ queryKey: ["dla"] });
      const previousBalance = queryClient.getQueryData<DLABalance>(
        queryKeys.dla.balance(),
      );
      const previousLedger = queryClient.getQueryData<InfiniteData<DLALedger>>(
        queryKeys.dla.ledgerPages(),
      );

      if (previousBalance) {
        queryClient.setQueryData<DLABalance>(
          queryKeys.dla.balance(),
          optimisticBalance(previousBalance, entry),
        );
      }
      if (previousLedger && previousBalance) {
        queryClient.setQueryData<InfiniteData<DLALedger>>(
          queryKeys.dla.ledgerPages(),
          optimisticLedger(previousLedger, previousBalance, entry),
        );
      }

      return { previousBalance, previousLedger };
    },
    onSettled: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.dla.balance() }),
        queryClient.invalidateQueries({
          queryKey: queryKeys.dla.ledgerPages(),
        }),
      ]);
    },
    onSuccess: () => {
      setForm(initialFormState());
    },
  });

  const balance = balanceQuery.data;
  const entries = useMemo(
    () => ledgerQuery.data?.pages.flatMap((page) => page.entries) ?? [],
    [ledgerQuery.data],
  );
  const directorName =
    profileQuery.data?.shareholders?.[0]?.name?.trim() || "Director";
  const currentBalanceText = balance
    ? formatBalance(
        balance.balance,
        balance.status === "overdrawn" ? "DR" : "CR",
      )
    : "Loading";
  const submitProblem = isApiError(createEntryMutation.error)
    ? createEntryMutation.error.problem
    : null;

  function updateForm<Key extends keyof EntryFormState>(
    key: Key,
    value: EntryFormState[Key],
  ) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const amountMinor = decimalAmountToMinor(form.amount);
    if (amountMinor <= 0) {
      return;
    }

    const entry: DLAEntryRequest = {
      amount: { amount_minor: amountMinor, currency: "GBP" },
      date: form.date,
      description: form.description.trim(),
      kind: form.kind,
      ...(form.kind === "repayment"
        ? { cash_account_code: repaymentCashAccount }
        : { expense_category: form.category }),
    };

    createEntryMutation.mutate(entry);
  }

  function clearWithDividend() {
    if (!balance?.suggested_clearance) {
      return;
    }
    navigate(
      `/dividends?amount=${encodeURIComponent(
        formatAmountForQuery(balance.suggested_clearance),
      )}`,
    );
  }

  return (
    <div className="dla-screen">
      <div className="dla-screen__header">
        <div>
          <PageTitle>Director&apos;s loan · {directorName}</PageTitle>
          <p className="type-secondary">
            Running ledger — positive means the company owes you
          </p>
        </div>
        <Button
          onClick={() => amountRef.current?.focus()}
          size="small"
          type="button"
          variant="secondary"
        >
          Record entry
        </Button>
      </div>

      <SplitMain>
        <div className="dla-main">
          <AdvisorStrip surface="dla" />

          {balance ? (
            <BalanceBanner balance={balance} text={currentBalanceText} />
          ) : (
            <div className="dla-balance-banner" role="status">
              <span>Current balance</span>
              <strong className="type-mono-numeral">Loading</strong>
            </div>
          )}

          <LedgerTable
            entries={entries}
            hasNextPage={ledgerQuery.hasNextPage}
            isFetchingNextPage={ledgerQuery.isFetchingNextPage}
            isLoading={ledgerQuery.isPending}
            onLoadMore={() => {
              void ledgerQuery.fetchNextPage();
            }}
          />
        </div>

        <aside className="dla-rail" aria-label="DLA status and manual entry">
          <StatusRail
            balance={balance}
            currentBalanceText={currentBalanceText}
            onClearWithDividend={clearWithDividend}
          />

          <Card title="Manual entry">
            <form className="dla-entry-form" onSubmit={handleSubmit}>
              <Field label="Entry kind">
                <Select
                  name="kind"
                  onChange={(event) =>
                    updateForm("kind", event.target.value as ManualKind)
                  }
                  value={form.kind}
                >
                  <option value="repayment">Repayment</option>
                  <option value="expense-owed">Expense owed</option>
                </Select>
              </Field>
              <Field label="Amount">
                <Input
                  min="0.01"
                  name="amount"
                  onChange={(event) => updateForm("amount", event.target.value)}
                  placeholder="0.00"
                  ref={amountRef}
                  required
                  step="0.01"
                  type="number"
                  value={form.amount}
                />
              </Field>
              <Field label="Date">
                <Input
                  name="date"
                  onChange={(event) => updateForm("date", event.target.value)}
                  required
                  type="date"
                  value={form.date}
                />
              </Field>
              <Field label="Description">
                <Input
                  name="description"
                  onChange={(event) =>
                    updateForm("description", event.target.value)
                  }
                  required
                  value={form.description}
                />
              </Field>
              {form.kind === "expense-owed" ? (
                <Field label="Category">
                  <Select
                    name="expense_category"
                    onChange={(event) =>
                      updateForm("category", event.target.value)
                    }
                    value={form.category}
                  >
                    {expenseCategories.map((category) => (
                      <option key={category.value} value={category.value}>
                        {category.label}
                      </option>
                    ))}
                  </Select>
                </Field>
              ) : null}
              {submitProblem ? (
                <div className="problem-alert" role="alert">
                  <strong>{submitProblem.title}</strong>
                  {submitProblem.detail ? (
                    <span>{submitProblem.detail}</span>
                  ) : null}
                </div>
              ) : null}
              <Button disabled={createEntryMutation.isPending} type="submit">
                {createEntryMutation.isPending ? "Recording" : "Record entry"}
              </Button>
            </form>
          </Card>
        </aside>
      </SplitMain>
    </div>
  );
}

function BalanceBanner({
  balance,
  text,
}: {
  readonly balance: DLABalance;
  readonly text: string;
}) {
  const isOverdrawn = balance.status === "overdrawn";
  return (
    <div
      className={
        isOverdrawn
          ? "dla-balance-banner dla-balance-banner--overdrawn"
          : "dla-balance-banner dla-balance-banner--credit"
      }
      role="status"
    >
      <span>
        {isOverdrawn
          ? "Current balance — director owes the company"
          : "Current balance — company owes you"}
      </span>
      <strong className="type-mono-numeral">{text}</strong>
    </div>
  );
}

function LedgerTable({
  entries,
  hasNextPage,
  isFetchingNextPage,
  isLoading,
  onLoadMore,
}: {
  readonly entries: DLAEntry[];
  readonly hasNextPage: boolean;
  readonly isFetchingNextPage: boolean;
  readonly isLoading: boolean;
  readonly onLoadMore: () => void;
}) {
  if (isLoading) {
    return (
      <Card title="Running ledger">
        <p className="type-secondary">Loading DLA ledger.</p>
      </Card>
    );
  }

  if (entries.length === 0) {
    return (
      <EmptyState>
        All caught up — there are no director loan entries to review.
      </EmptyState>
    );
  }

  return (
    <Table aria-label="DLA running ledger" className="dla-ledger-table">
      <TableHead>
        <TableRow>
          <TableHeaderCell>Date</TableHeaderCell>
          <TableHeaderCell>Entry</TableHeaderCell>
          <TableHeaderCell>Kind</TableHeaderCell>
          <TableHeaderCell align="right">Owed to you</TableHeaderCell>
          <TableHeaderCell align="right">Drawn</TableHeaderCell>
          <TableHeaderCell align="right">Balance</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {entries.map((entry) => (
          <TableRow key={`${entry.id}-${entry.source_ref}`}>
            <TableCell className="dla-ledger-table__date">
              {formatDate(entry.date)}
            </TableCell>
            <TableCell>{entry.description}</TableCell>
            <TableCell>{formatKind(entry.kind)}</TableCell>
            <TableCell align="right" variant="mono-numeric">
              {formatMoneyOrDash(entry.owed_to_you)}
            </TableCell>
            <TableCell align="right" variant="mono-numeric">
              {formatMoneyOrDash(entry.drawn)}
            </TableCell>
            <TableCell align="right" variant="mono-numeric">
              {formatBalance(entry.running_balance, entry.balance_side)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
      {hasNextPage ? (
        <TableFooter>
          <TableRow>
            <TableCell colSpan={6}>
              <Button
                disabled={isFetchingNextPage}
                onClick={onLoadMore}
                size="small"
                type="button"
                variant="secondary"
              >
                {isFetchingNextPage ? "Loading" : "Load more"}
              </Button>
            </TableCell>
          </TableRow>
        </TableFooter>
      ) : null}
    </Table>
  );
}

function StatusRail({
  balance,
  currentBalanceText,
  onClearWithDividend,
}: {
  readonly balance: DLABalance | undefined;
  readonly currentBalanceText: string;
  readonly onClearWithDividend: () => void;
}) {
  if (!balance) {
    return (
      <Card title="Status">
        <p className="type-secondary">Loading DLA status.</p>
      </Card>
    );
  }

  if (balance.status === "overdrawn") {
    return (
      <Card
        className="dla-status-card dla-status-card--overdrawn"
        title="Status"
      >
        <p className="type-uppercase-label">Edge state — if overdrawn</p>
        <p className="type-body">
          {renderTemplate(balance.policy.overdrawn_warning_template, {
            balance: formatMoney(
              balance.suggested_clearance ?? balance.balance,
            ),
          })}
        </p>
        <Button onClick={onClearWithDividend} size="small" type="button">
          Clear with dividend →
        </Button>
      </Card>
    );
  }

  return (
    <Card className="dla-status-card" title="Status">
      <div className="dla-status-card__headline">
        <span aria-hidden="true" className="dla-status-card__dot" />
        <strong>{balance.policy.credit_status_text}</strong>
      </div>
      <p className="type-secondary">
        {renderTemplate(balance.policy.credit_explainer_template, {
          balance: currentBalanceText.replace(/\sCR$/, ""),
        })}
      </p>
    </Card>
  );
}

function optimisticBalance(
  current: DLABalance,
  entry: DLAEntryRequest,
): DLABalance {
  const nextBalance = {
    ...current.balance,
    amount_minor: current.balance.amount_minor + entry.amount.amount_minor,
  };
  const status = nextBalance.amount_minor < 0 ? "overdrawn" : "credit";
  return {
    ...current,
    balance: nextBalance,
    status,
    suggested_clearance:
      status === "overdrawn"
        ? { ...nextBalance, amount_minor: Math.abs(nextBalance.amount_minor) }
        : null,
  };
}

function optimisticLedger(
  current: InfiniteData<DLALedger>,
  balance: DLABalance,
  entry: DLAEntryRequest,
): InfiniteData<DLALedger> {
  if (current.pages.length === 0) {
    return current;
  }

  const lastPageIndex = current.pages.length - 1;
  const nextBalance = optimisticBalance(balance, entry).balance;
  const optimisticEntry: DLAEntry = {
    amount: entry.amount,
    balance_side: balanceSideFor(nextBalance.amount_minor),
    created_at: new Date().toISOString(),
    date: entry.date,
    description: entry.description,
    drawn: { amount_minor: 0, currency: entry.amount.currency },
    id: -Date.now(),
    kind: entry.kind,
    owed_to_you: entry.amount,
    running_balance: nextBalance,
    source_ref: "manual:pending",
  };

  return {
    ...current,
    pages: current.pages.map((page, index) =>
      index === lastPageIndex
        ? { ...page, entries: [...page.entries, optimisticEntry] }
        : page,
    ),
  };
}

function decimalAmountToMinor(value: string) {
  const amount = Number(value);
  if (!Number.isFinite(amount)) {
    return 0;
  }
  return Math.round(amount * 100);
}

function formatAmountForQuery(value: DLAMoney) {
  return (Math.abs(value.amount_minor) / 100).toFixed(2);
}

function formatBalance(
  value: DLAMoney,
  side: DLAEntry["balance_side"] | "CR" | "DR",
) {
  const suffix = side === "zero" ? "CR" : side;
  return `${formatMoney(value)} ${suffix}`;
}

function formatMoney(value: DLAMoney) {
  return formatMinorUnits({
    amountMinor: Math.abs(value.amount_minor),
    currency: value.currency,
  });
}

function formatMoneyOrDash(value: DLAMoney) {
  if (value.amount_minor === 0) {
    return "—";
  }
  return formatMoney(value);
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "2-digit",
    month: "short",
    timeZone: "UTC",
  }).format(new Date(`${value}T00:00:00Z`));
}

function formatKind(kind: DLAEntry["kind"]) {
  switch (kind) {
    case "drawing":
      return "Drawing";
    case "expense-owed":
      return "Expense owed";
    case "repayment":
      return "Repayment";
  }
}

function balanceSideFor(amountMinor: number): DLAEntry["balance_side"] {
  if (amountMinor < 0) {
    return "DR";
  }
  if (amountMinor > 0) {
    return "CR";
  }
  return "zero";
}

function renderTemplate(template: string, values: Record<string, string>) {
  return template.replace(/\{\{\s*([a-z_]+)\s*\}\}/g, (_match, key: string) => {
    return values[key] ?? "";
  });
}
