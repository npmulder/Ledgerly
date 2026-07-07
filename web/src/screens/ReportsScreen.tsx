import { type FormEvent, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";

import { queryKeys } from "@/api/queryKeys";
import {
  getReportsCalendar,
  getReportsPL,
  getReportsVAT,
  reportsExportURL,
  shareReportsExport,
  type ReportsFiling,
  type ReportsMoney,
  type ReportsPL,
  type ReportsVAT,
} from "@/api/reports";
import {
  AdvisorStrip,
  Button,
  Card,
  Field,
  Input,
  PageTitle,
  formatMinorUnits,
} from "@/components";

type ReportsPeriod = {
  readonly from: string;
  readonly id: string;
  readonly label: string;
  readonly to: string;
  readonly vatPeriod: string;
};

const quarterDefinitions = [
  { label: "Jan-Mar", quarter: 1, toMonth: 2 },
  { label: "Apr-Jun", quarter: 2, toMonth: 5 },
  { label: "Jul-Sep", quarter: 3, toMonth: 8 },
  { label: "Oct-Dec", quarter: 4, toMonth: 11 },
];

export function ReportsScreen() {
  const quarterPresets = useMemo(() => buildQuarterPresets(), []);
  const defaultPeriod = defaultReportsPeriod(quarterPresets);
  const [period, setPeriod] = useState<ReportsPeriod>(() => defaultPeriod);
  const [customRange, setCustomRange] = useState(() => ({
    from: defaultPeriod.from,
    to: defaultPeriod.to,
  }));
  const [shareOpen, setShareOpen] = useState(false);
  const [shareEmail, setShareEmail] = useState("");
  const [statusToast, setStatusToast] = useState<{
    readonly kind: "error" | "success";
    readonly text: string;
  } | null>(null);

  const plQuery = useQuery({
    queryFn: () => getReportsPL(period.from, period.to),
    queryKey: queryKeys.reports.pl(period.from, period.to),
  });
  const vatQuery = useQuery({
    queryFn: () => getReportsVAT(period.vatPeriod),
    queryKey: queryKeys.reports.vat(period.vatPeriod),
  });
  const calendarQuery = useQuery({
    queryFn: getReportsCalendar,
    queryKey: queryKeys.reports.calendar(),
  });

  const vatFiling = useMemo(() => {
    const dueDate = dueDateForVATPeriod(period.vatPeriod);
    if (!dueDate) {
      return undefined;
    }
    return calendarQuery.data?.filings.find(
      (filing) => filing.key === "vat_return" && filing.due_date === dueDate,
    );
  }, [calendarQuery.data, period.vatPeriod]);

  const shareMutation = useMutation({
    mutationFn: () => shareReportsExport(shareEmail, period.from, period.to),
    onError: (error) => {
      setStatusToast({
        kind: "error",
        text:
          error instanceof Error
            ? error.message
            : "Unable to share export pack.",
      });
    },
    onSuccess: (result) => {
      setShareOpen(false);
      setStatusToast({
        kind: "success",
        text: result.message,
      });
    },
  });

  function selectPreset(next: ReportsPeriod) {
    setPeriod(next);
    setCustomRange({ from: next.from, to: next.to });
  }

  function applyCustomRange(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!isCompleteCustomRange(customRange)) {
      return;
    }
    setPeriod({
      from: customRange.from,
      id: "custom",
      label: formatRangeLabel(customRange.from, customRange.to),
      to: customRange.to,
      vatPeriod: quarterForDate(customRange.from),
    });
  }

  function exportPack() {
    const link = document.createElement("a");
    link.href = reportsExportURL(period.from, period.to);
    link.download = `ledgerly-export-${period.from}_${period.to}.zip`;
    document.body.append(link);
    link.click();
    link.remove();
    setStatusToast({ kind: "success", text: "Export pack is being prepared." });
  }

  function submitShare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    shareMutation.mutate();
  }

  return (
    <div className="reports-screen">
      <div className="reports-screen__header">
        <PageTitle>Reports</PageTitle>
        <div className="reports-screen__actions">
          <Button
            type="button"
            variant="secondary"
            onClick={() => setShareOpen(true)}
          >
            Share with accountant
          </Button>
          <Button type="button" onClick={exportPack}>
            Export pack
          </Button>
        </div>
      </div>

      {statusToast ? (
        <div
          className={`reports-toast reports-toast--${statusToast.kind}`}
          role="status"
        >
          {statusToast.text}
        </div>
      ) : null}

      {shareOpen ? (
        <div className="reports-modal-backdrop">
          <form className="reports-share-modal" onSubmit={submitShare}>
            <header>
              <h2>Share with accountant</h2>
            </header>
            <Field label="Accountant email">
              <Input
                autoComplete="email"
                inputMode="email"
                onChange={(event) => setShareEmail(event.target.value)}
                required
                type="email"
                value={shareEmail}
              />
            </Field>
            <div className="reports-share-modal__actions">
              <Button
                disabled={shareMutation.isPending}
                onClick={() => setShareOpen(false)}
                type="button"
                variant="secondary"
              >
                Cancel
              </Button>
              <Button disabled={shareMutation.isPending} type="submit">
                {shareMutation.isPending ? "Sharing" : "Share"}
              </Button>
            </div>
          </form>
        </div>
      ) : null}

      <AdvisorStrip surface="reports" />

      <div className="reports-layout">
        <ProfitAndLossCard
          isLoading={plQuery.isPending}
          period={period}
          pl={plQuery.data}
          onSelectPreset={selectPreset}
          customRange={customRange}
          onCustomRangeChange={setCustomRange}
          onCustomRangeSubmit={applyCustomRange}
          quarterPresets={quarterPresets}
        />
        <aside className="reports-rail" aria-label="VAT and filing calendar">
          <VATReturnCard
            filing={vatFiling}
            isLoading={vatQuery.isPending || calendarQuery.isPending}
            period={period}
            vat={vatQuery.data}
          />
          <FilingCalendarCard
            filings={calendarQuery.data?.filings ?? []}
            isLoading={calendarQuery.isPending}
          />
        </aside>
      </div>
    </div>
  );
}

function ProfitAndLossCard({
  customRange,
  isLoading,
  onCustomRangeChange,
  onCustomRangeSubmit,
  onSelectPreset,
  period,
  pl,
  quarterPresets,
}: {
  readonly customRange: { readonly from: string; readonly to: string };
  readonly isLoading: boolean;
  readonly onCustomRangeChange: (range: { from: string; to: string }) => void;
  readonly onCustomRangeSubmit: (event: FormEvent<HTMLFormElement>) => void;
  readonly onSelectPreset: (period: ReportsPeriod) => void;
  readonly period: ReportsPeriod;
  readonly pl: ReportsPL | undefined;
  readonly quarterPresets: ReportsPeriod[];
}) {
  return (
    <Card
      className="reports-pl-card"
      title={
        <div className="reports-card-title">
          <span>
            Profit &amp; loss · {period.label} {periodYearLabel(period)}
          </span>
          <span>GBP · presentational currency</span>
        </div>
      }
    >
      <div className="reports-period-control" aria-label="Report period">
        <div className="reports-period-control__presets">
          {quarterPresets.map((preset) => (
            <button
              aria-pressed={period.id === preset.id}
              className={
                period.id === preset.id
                  ? "reports-period-pill reports-period-pill--active"
                  : "reports-period-pill"
              }
              key={preset.id}
              onClick={() => onSelectPreset(preset)}
              type="button"
            >
              {preset.label}
            </button>
          ))}
        </div>
        <form
          aria-label="Custom report range"
          className="reports-custom-range"
          onSubmit={onCustomRangeSubmit}
        >
          <Field label="From">
            <Input
              max={customRange.to}
              name="from"
              onChange={(event) =>
                onCustomRangeChange({
                  ...customRange,
                  from: event.target.value,
                })
              }
              required
              type="date"
              value={customRange.from}
            />
          </Field>
          <Field label="To">
            <Input
              min={customRange.from}
              name="to"
              onChange={(event) =>
                onCustomRangeChange({
                  ...customRange,
                  to: event.target.value,
                })
              }
              required
              type="date"
              value={customRange.to}
            />
          </Field>
          <Button size="small" type="submit" variant="secondary">
            Apply
          </Button>
        </form>
      </div>

      {isLoading ? (
        <p className="type-secondary">Loading profit and loss.</p>
      ) : pl ? (
        <div className="reports-line-list" role="table" aria-label="P&L lines">
          {pl.income.map((line) => (
            <ReportLine
              amount={line.amount}
              key={`${line.client_id || line.label}-${line.currency}`}
              label={line.label}
            />
          ))}
          <ReportLine
            amount={pl.realised_fx_gains.amount}
            label="Realised FX gains on settlement"
            tone={
              pl.realised_fx_gains.amount.amount_minor >= 0
                ? "positive"
                : "default"
            }
          />
          <ReportLine amount={pl.income_total} label="Turnover" strong />
          {pl.expenses.map((line) => (
            <ReportLine
              amount={line.amount}
              key={line.account_code}
              label={line.account_name}
              negative
            />
          ))}
          <ReportLine
            amount={pl.profit_before_tax}
            label="Profit before tax"
            strong
          />
          <ReportLine
            amount={pl.corporate_tax.amount}
            label={pl.corporate_tax.label}
            muted
            negative
          />
          <ReportLine
            amount={pl.net_profit}
            label="Net profit for the period"
            rule
            strong
          />
        </div>
      ) : (
        <p className="type-secondary">Unable to load profit and loss.</p>
      )}
    </Card>
  );
}

function VATReturnCard({
  filing,
  isLoading,
  period,
  vat,
}: {
  readonly filing: ReportsFiling | undefined;
  readonly isLoading: boolean;
  readonly period: ReportsPeriod;
  readonly vat: ReportsVAT | undefined;
}) {
  const hasFigures = hasVATFigures(vat);
  const reclaim = hasFigures && vat.net_position.amount_minor < 0;

  return (
    <Card
      className="reports-vat-card"
      title={<span>VAT return · {formatVATPeriodLabel(period.vatPeriod)}</span>}
      actions={
        filing && hasFigures ? <DueBadge filing={filing} prefix="DUE" /> : null
      }
    >
      {isLoading ? (
        <p className="type-secondary">Loading VAT return.</p>
      ) : vat?.status === "not_registered" ? (
        <p className="type-secondary">Not VAT registered.</p>
      ) : hasFigures ? (
        <>
          <div className="reports-vat-lines">
            <ReportLine amount={vat.box1} label="Box 1 · VAT due on sales" />
            <ReportLine amount={vat.box4} label="Box 4 · VAT reclaimed" />
            <ReportLine amount={vat.box6} label="Box 6 · Total sales ex-VAT" />
            <ReportLine
              amount={{
                ...vat.net_position,
                amount_minor: Math.abs(vat.net_position.amount_minor),
              }}
              label={
                reclaim ? "Net reclaim from IoM C&E" : "Net payable to IoM C&E"
              }
              rule
              strong
              tone={reclaim ? "positive" : "default"}
            />
          </div>
          <p className="reports-card-note">
            EU B2B services are outside scope - reverse charge. Filed with Isle
            of Man Customs &amp; Excise.
          </p>
        </>
      ) : (
        <p className="type-secondary">Unable to load VAT return.</p>
      )}
    </Card>
  );
}

function hasVATFigures(vat: ReportsVAT | undefined): vat is ReportsVAT & {
  readonly box1: ReportsMoney;
  readonly box4: ReportsMoney;
  readonly box6: ReportsMoney;
  readonly net_position: ReportsMoney;
} {
  return (
    vat?.status === "registered" &&
    vat.box1 !== undefined &&
    vat.box4 !== undefined &&
    vat.box6 !== undefined &&
    vat.net_position !== undefined
  );
}

function FilingCalendarCard({
  filings,
  isLoading,
}: {
  readonly filings: ReportsFiling[];
  readonly isLoading: boolean;
}) {
  return (
    <Card
      className="reports-calendar-card"
      title="Filing calendar · Isle of Man"
    >
      {isLoading ? (
        <p className="type-secondary">Loading filing calendar.</p>
      ) : (
        <div className="reports-calendar-list" role="list">
          {filings.map((filing) => (
            <div
              className="reports-calendar-row"
              key={filing.key}
              role="listitem"
            >
              <div className="reports-calendar-row__label">
                <strong>{filing.label}</strong>
                <span>{filing.authority}</span>
              </div>
              <DueBadge filing={filing} />
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}

type ReportLineTone = "default" | "positive";

function ReportLine({
  amount,
  label,
  muted = false,
  negative = false,
  rule = false,
  strong = false,
  tone = "default",
}: {
  readonly amount: ReportsMoney;
  readonly label: string;
  readonly muted?: boolean;
  readonly negative?: boolean;
  readonly rule?: boolean;
  readonly strong?: boolean;
  readonly tone?: ReportLineTone;
}) {
  return (
    <div
      className={[
        "reports-line",
        muted ? "reports-line--muted" : "",
        rule ? "reports-line--rule" : "",
        strong ? "reports-line--strong" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      role="row"
    >
      <span role="cell">{label}</span>
      <span
        className={
          tone === "positive"
            ? "reports-money reports-money--positive"
            : "reports-money"
        }
        role="cell"
      >
        {formatReportMoney(amount, negative)}
      </span>
    </div>
  );
}

function DueBadge({
  filing,
  prefix,
}: {
  readonly filing: ReportsFiling;
  readonly prefix?: string;
}) {
  return (
    <span
      aria-label={`${filing.label} ${filing.status} ${formatDateBadge(
        filing.due_date,
      )}`}
      className={`reports-due-badge reports-due-badge--${filing.status}`}
    >
      {[
        prefix,
        filing.status === "overdue" ? "OVERDUE" : null,
        formatDateBadge(filing.due_date),
      ]
        .filter(Boolean)
        .join(" ")}
    </span>
  );
}

function formatReportMoney(value: ReportsMoney, negative = false) {
  const formatted = formatMinorUnits({
    amountMinor: negative ? Math.abs(value.amount_minor) : value.amount_minor,
    currency: value.currency,
  });
  if (negative && value.amount_minor !== 0) {
    return `(${formatted})`;
  }
  return formatted;
}

function formatDateBadge(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "2-digit",
    month: "short",
    timeZone: "UTC",
  })
    .format(new Date(`${value}T00:00:00Z`))
    .toUpperCase();
}

function formatRangeLabel(from: string, to: string) {
  return `${formatShortMonth(from)}-${formatShortMonth(to)}`;
}

function formatShortMonth(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    month: "short",
    timeZone: "UTC",
  }).format(new Date(`${value}T00:00:00Z`));
}

function quarterForDate(value: string) {
  const date = new Date(`${value}T00:00:00Z`);
  const quarter = Math.floor(date.getUTCMonth() / 3) + 1;
  return `${date.getUTCFullYear()}-Q${quarter}`;
}

function buildQuarterPresets(year = new Date().getUTCFullYear()) {
  return quarterDefinitions.map((definition) => {
    const fromMonth = definition.toMonth - 2;
    const vatPeriod = `${year}-Q${definition.quarter}`;
    return {
      from: isoDate(year, fromMonth, 1),
      id: vatPeriod,
      label: definition.label,
      to: isoDate(
        year,
        definition.toMonth,
        daysInMonth(year, definition.toMonth),
      ),
      vatPeriod,
    };
  });
}

function defaultReportsPeriod(presets: readonly ReportsPeriod[]) {
  const defaultPeriod = presets[1];
  if (!defaultPeriod) {
    throw new Error("reports quarter presets are not configured");
  }
  return defaultPeriod;
}

function periodYearLabel(period: ReportsPeriod) {
  const fromYear = Number(period.from.slice(0, 4));
  const toYear = Number(period.to.slice(0, 4));
  return fromYear === toYear ? String(fromYear) : `${fromYear}-${toYear}`;
}

function isCompleteCustomRange({
  from,
  to,
}: {
  readonly from: string;
  readonly to: string;
}) {
  return from !== "" && to !== "" && from <= to;
}

function formatVATPeriodLabel(vatPeriod: string) {
  const parsed = parseVATPeriod(vatPeriod);
  if (!parsed) {
    return vatPeriod;
  }
  const definition = quarterDefinition(parsed.quarter);
  return `${definition.label} ${parsed.year}`;
}

function dueDateForVATPeriod(vatPeriod: string) {
  const parsed = parseVATPeriod(vatPeriod);
  if (!parsed) {
    return null;
  }
  const definition = quarterDefinition(parsed.quarter);
  const quarterEndDay = daysInMonth(parsed.year, definition.toMonth);
  return addMonthsClamped(parsed.year, definition.toMonth, quarterEndDay, 1);
}

function parseVATPeriod(vatPeriod: string) {
  const match = /^([0-9]{4})-Q([1-4])$/.exec(vatPeriod);
  if (!match) {
    return null;
  }
  return {
    quarter: Number(match[2]),
    year: Number(match[1]),
  };
}

function quarterDefinition(quarter: number) {
  const definition = quarterDefinitions.find(
    (candidate) => candidate.quarter === quarter,
  );
  if (!definition) {
    throw new Error(`unsupported VAT quarter ${quarter}`);
  }
  return definition;
}

function addMonthsClamped(
  year: number,
  monthIndex: number,
  day: number,
  months: number,
) {
  const targetMonthIndex = monthIndex + months;
  const targetYear = year + Math.floor(targetMonthIndex / 12);
  const targetMonth = targetMonthIndex % 12;
  return isoDate(
    targetYear,
    targetMonth,
    Math.min(day, daysInMonth(targetYear, targetMonth)),
  );
}

function daysInMonth(year: number, monthIndex: number) {
  return new Date(Date.UTC(year, monthIndex + 1, 0)).getUTCDate();
}

function isoDate(year: number, monthIndex: number, day: number) {
  return new Date(Date.UTC(year, monthIndex, day)).toISOString().slice(0, 10);
}
