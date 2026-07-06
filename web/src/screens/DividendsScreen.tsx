import { type FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";

import {
  boardMinutesPDFPath,
  declareDividendAmount,
  dividendVoucherPDFPath,
  getDividendHeadroom,
  getDividendHistory,
  validateDividendAmount,
  type DividendsDeclaration,
  type DividendsHeadroom,
  type DividendsMoney,
  type DividendsValidationResult,
} from "@/api/dividends";
import { queryKeys } from "@/api/queryKeys";
import {
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  PageTitle,
  SplitMain,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
  formatMinorUnits,
} from "@/components";

const validationDebounceMs = 300;

export function DividendsScreen() {
  const [searchParams] = useSearchParams();
  const queryClient = useQueryClient();
  const [amountInput, setAmountInput] = useState(
    () => searchParams.get("amount")?.trim() ?? "",
  );
  const [debouncedAmountMinor, setDebouncedAmountMinor] = useState<number | null>(
    () => parseAmountToMinor(searchParams.get("amount")?.trim() ?? ""),
  );
  const [submitStatus, setSubmitStatus] = useState<string | null>(null);

  const amountMinor = useMemo(
    () => parseAmountToMinor(amountInput),
    [amountInput],
  );
  const amountMoney = useMemo(
    () =>
      amountMinor === null
        ? null
        : ({
            amount: amountMinor,
            currency: "GBP",
          } satisfies DividendsMoney),
    [amountMinor],
  );

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      setDebouncedAmountMinor(amountMinor);
    }, validationDebounceMs);
    return () => window.clearTimeout(timeout);
  }, [amountMinor]);

  const headroomQuery = useQuery({
    queryFn: getDividendHeadroom,
    queryKey: queryKeys.dividends.headroom(),
  });
  const historyQuery = useQuery({
    queryFn: getDividendHistory,
    queryKey: queryKeys.dividends.history(),
  });
  const validationQuery = useQuery({
    enabled: debouncedAmountMinor !== null && debouncedAmountMinor > 0,
    queryFn: () =>
      validateDividendAmount({
        amount: debouncedAmountMinor ?? 0,
        currency: "GBP",
      }),
    queryKey: queryKeys.dividends.validation(debouncedAmountMinor),
    retry: false,
  });
  const declareMutation = useMutation({
    mutationFn: declareDividendAmount,
    onSuccess: async () => {
      setSubmitStatus("Dividend declared");
      await queryClient.invalidateQueries({ queryKey: ["dividends"] });
    },
  });

  const history = historyQuery.data?.declarations ?? [];
  const latestDeclaration = history[0] ?? null;
  const validation = validationQuery.data ?? null;
  const canDeclare =
    amountMoney !== null &&
    validation?.within_headroom === true &&
    !declareMutation.isPending;

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitStatus(null);
    if (!canDeclare || amountMoney === null) {
      return;
    }
    declareMutation.mutate(amountMoney);
  }

  return (
    <div className="dividends-screen">
      <div className="dividends-screen__header">
        <div>
          <PageTitle>Dividends</PageTitle>
          <p className="type-secondary">
            Headroom, declaration paperwork, and director loan credit
          </p>
        </div>
      </div>

      <SplitMain>
        <div className="dividends-main">
          <HeadroomPanel
            headroom={headroomQuery.data ?? null}
            isLoading={headroomQuery.isPending}
          />

          <form className="dividends-declare" onSubmit={handleSubmit}>
            <Card
              actions={
                <Button disabled={!canDeclare} type="submit">
                  Generate voucher + minutes
                </Button>
              }
              title="Declare dividend"
            >
              <div className="dividends-declare__grid">
                <Field label="Amount">
                  <Input
                    inputMode="decimal"
                    invalid={amountInput.trim() !== "" && amountMinor === null}
                    onChange={(event) => {
                      setSubmitStatus(null);
                      setAmountInput(event.target.value);
                    }}
                    placeholder="0.00"
                    value={amountInput}
                  />
                </Field>
                <ValidationStrip
                  amountMinor={amountMinor}
                  isLoading={validationQuery.isFetching}
                  validation={validation}
                />
              </div>
              {declareMutation.isError ? (
                <p className="dividends-submit-state" role="alert">
                  Unable to declare this dividend.
                </p>
              ) : null}
              {submitStatus ? (
                <p className="dividends-submit-state" role="status">
                  {submitStatus}
                </p>
              ) : null}
            </Card>
          </form>

          <HistoryTable
            declarations={history}
            isLoading={historyQuery.isPending}
          />
        </div>

        <aside className="dividends-rail" aria-label="Dividend document previews">
          <DocumentPreviews declaration={latestDeclaration} />
        </aside>
      </SplitMain>
    </div>
  );
}

function HeadroomPanel({
  headroom,
  isLoading,
}: {
  readonly headroom: DividendsHeadroom | null;
  readonly isLoading: boolean;
}) {
  const available = headroom?.available ?? null;
  const negative = Boolean(available && available.amount < 0);

  return (
    <Card
      className={negative ? "dividends-headroom dividends-headroom--negative" : "dividends-headroom"}
      title="Headroom calculation"
    >
      {headroom ? (
        <div>
          <dl aria-label="Dividend headroom breakdown">
            {headroom.lines.map((line, index) => {
              const totalLine =
                line.label === "Available to distribute" ||
                index === headroom.lines.length - 1;
              return (
                <div
                  className={
                    totalLine
                      ? "dividends-headroom__line dividends-headroom__line--total"
                      : "dividends-headroom__line"
                  }
                  key={line.label}
                >
                  <dt>{line.label}</dt>
                  <dd className="type-mono-numeral">
                    {formatDividendMoney(line.amount)}
                  </dd>
                </div>
              );
            })}
          </dl>
          <p className="dividends-headroom__state" role="status">
            {headroom.distributable
              ? "Available for declaration"
              : "Not currently distributable"}
          </p>
        </div>
      ) : (
        <p className="type-secondary">
          {isLoading ? "Loading headroom" : "Headroom unavailable"}
        </p>
      )}
    </Card>
  );
}

function ValidationStrip({
  amountMinor,
  isLoading,
  validation,
}: {
  readonly amountMinor: number | null;
  readonly isLoading: boolean;
  readonly validation: DividendsValidationResult | null;
}) {
  if (amountMinor === null || amountMinor <= 0) {
    return (
      <div className="dividends-validation-strip" role="status">
        <span>Enter a dividend amount</span>
      </div>
    );
  }
  if (isLoading && !validation) {
    return (
      <div className="dividends-validation-strip" role="status">
        <span>Validating</span>
      </div>
    );
  }
  if (!validation) {
    return (
      <div className="dividends-validation-strip" role="status">
        <span>Validation unavailable</span>
      </div>
    );
  }

  if (!validation.within_headroom) {
    return (
      <div
        className="dividends-validation-strip dividends-validation-strip--blocked"
        role="alert"
      >
        <span>Over headroom</span>
        <span>
          Distributable figure{" "}
          <strong>{formatDividendMoney(validation.distributable_total)}</strong>
        </span>
      </div>
    );
  }

  return (
    <div className="dividends-validation-strip dividends-validation-strip--ok" role="status">
      <span>Within headroom ✓</span>
      <span>{validation.withholding.applies ? "WHT applies" : "No WHT ✓"}</span>
      <span>{validation.personal_tax.message}</span>
    </div>
  );
}

function HistoryTable({
  declarations,
  isLoading,
}: {
  readonly declarations: DividendsDeclaration[];
  readonly isLoading: boolean;
}) {
  if (declarations.length === 0) {
    return (
      <EmptyState
        title="No dividend history"
      >
        {isLoading
          ? "Loading declaration history"
          : "Declared dividends will appear here."}
      </EmptyState>
    );
  }

  return (
    <Table aria-label="Dividend history" className="dividends-history-table">
      <TableHead>
        <TableRow>
          <TableHeaderCell>Date</TableHeaderCell>
          <TableHeaderCell align="right">Amount</TableHeaderCell>
          <TableHeaderCell align="right">Per share</TableHeaderCell>
          <TableHeaderCell>Documents</TableHeaderCell>
        </TableRow>
      </TableHead>
      <TableBody>
        {declarations.map((declaration) => (
          <TableRow key={declaration.id}>
            <TableCell>{formatDividendDate(declaration.declared_date)}</TableCell>
            <TableCell align="right" variant="mono-numeric">
              {formatDividendMoney(declaration.amount)}
            </TableCell>
            <TableCell align="right" variant="mono-numeric">
              {formatDividendMoney(declaration.per_share)}
            </TableCell>
            <TableCell>
              <div className="dividends-history-table__links">
                <a href={dividendVoucherPDFPath(declaration.id)}>Voucher</a>
                <a href={boardMinutesPDFPath(declaration.id)}>Minutes</a>
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function DocumentPreviews({
  declaration,
}: {
  readonly declaration: DividendsDeclaration | null;
}) {
  if (!declaration) {
    return (
      <EmptyState title="No declaration selected">
        The latest declaration documents will render here.
      </EmptyState>
    );
  }

  return (
    <>
      <DocumentPreview
        id={declaration.id}
        kind="dividend-voucher"
        title="Dividend voucher preview"
      />
      <DocumentPreview
        id={declaration.id}
        kind="board-minutes"
        title="Board minutes preview"
      />
    </>
  );
}

function DocumentPreview({
  id,
  kind,
  title,
}: {
  readonly id: string;
  readonly kind: "board-minutes" | "dividend-voucher";
  readonly title: string;
}) {
  return (
    <Card className="dividends-document-preview" title={title}>
      <iframe
        src={`/print/${kind}/${encodeURIComponent(id)}`}
        title={title}
      />
    </Card>
  );
}

function parseAmountToMinor(value: string) {
  const normalized = value.trim().replace(/,/g, "");
  if (normalized === "") {
    return null;
  }
  if (!/^\d+(\.\d{0,2})?$/.test(normalized)) {
    return null;
  }
  const [major, minor = ""] = normalized.split(".");
  const pounds = Number.parseInt(major, 10);
  if (!Number.isSafeInteger(pounds)) {
    return null;
  }
  const pence = Number.parseInt(minor.padEnd(2, "0"), 10) || 0;
  const total = pounds * 100 + pence;
  return Number.isSafeInteger(total) ? total : null;
}

function formatDividendMoney(value: DividendsMoney) {
  return formatMinorUnits({
    amountMinor: value.amount,
    currency: value.currency,
  });
}

function formatDividendDate(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "2-digit",
    month: "short",
    timeZone: "UTC",
    year: "numeric",
  }).format(new Date(value));
}
