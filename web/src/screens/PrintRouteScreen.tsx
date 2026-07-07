import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useLocation } from "react-router-dom";

import {
  getInvoicePrintPayload,
  type InvoicingInvoicePrintPayload,
} from "@/api/invoicing";
import {
  getDividendDocumentPayload,
  type DividendsDocumentPayload,
} from "@/api/dividends";
import {
  getReportsPLPrintPayload,
  type ReportsPLPrintPayload,
} from "@/api/reports";

const invoicePrintStoragePrefix = "ledgerly.print.invoice.";
const dividendPrintStoragePrefix = "ledgerly.print.dividend.";
const reportsPLPrintStoragePrefix = "ledgerly.print.reports.pl.";

export function PrintRouteScreen() {
  const location = useLocation();
  const route = useMemo(() => printRoute(location.pathname), [location]);
  const draft = useMemo(
    () => new URLSearchParams(location.search).get("draft") === "1",
    [location.search],
  );
  const [storedPayload] = useState(() =>
    route.id ? readStoredPayload(route) : null,
  );

  const payloadQuery = useQuery<PrintPayload>({
    enabled: Boolean(route.id && !storedPayload),
    queryFn: () => fetchPrintPayload(route, draft),
    queryKey: ["print", route.kind, route.id, draft],
    retry: false,
  });

  const payload = storedPayload ?? payloadQuery.data ?? null;

  if (!route.id) {
    return (
      <main className="print-route print-route--state">
        <p>Print route not found</p>
      </main>
    );
  }

  if (!payload) {
    return (
      <main className="print-route print-route--state">
        <p>
          {payloadQuery.isError ? "Unable to load print document" : "Loading"}
        </p>
      </main>
    );
  }

  return (
    <main className="print-route">
      <PrintDocument payload={payload} route={route} />
    </main>
  );
}

type PrintRoute =
  | { kind: "invoice"; id: string }
  | { kind: "dividend-voucher"; id: string }
  | { kind: "board-minutes"; id: string }
  | { kind: "reports-pl"; id: string }
  | { kind: "unknown"; id: "" };

type PrintPayload =
  | InvoicingInvoicePrintPayload
  | DividendsDocumentPayload
  | ReportsPLPrintPayload;
type DividendCompanySnapshot = NonNullable<
  DividendsDocumentPayload["declaration"]["company_snapshot"]
>;

function PrintDocument({
  payload,
  route,
}: {
  payload: PrintPayload;
  route: PrintRoute;
}) {
  if (route.kind === "dividend-voucher") {
    return (
      <DividendVoucherDocument payload={payload as DividendsDocumentPayload} />
    );
  }
  if (route.kind === "board-minutes") {
    return (
      <BoardMinutesDocument payload={payload as DividendsDocumentPayload} />
    );
  }
  if (route.kind === "reports-pl") {
    return <ReportsPLDocument payload={payload as ReportsPLPrintPayload} />;
  }
  return <InvoiceDocument payload={payload as InvoicingInvoicePrintPayload} />;
}

function InvoiceDocument({
  payload,
}: {
  payload: InvoicingInvoicePrintPayload;
}) {
  const { client, identity, invoice } = payload;
  const invoiceNumber = invoice.number ?? "Draft invoice";
  const logoSrc = identity.logo_data_uri ?? identity.logo_asset_url ?? null;

  return (
    <article
      className="invoice-print"
      data-ledgerly-print-ready="true"
      aria-label={`Invoice ${invoiceNumber}`}
    >
      {payload.draft_watermark ? (
        <div className="invoice-print__watermark">DRAFT</div>
      ) : null}

      <header className="invoice-print__header">
        <div className="invoice-print__brand">
          {logoSrc ? (
            <img alt={identity.trading_name} src={logoSrc} />
          ) : (
            <div className="invoice-print__brand-mark">
              {initials(identity.trading_name)}
            </div>
          )}
          <div>
            <p className="invoice-print__eyebrow">Invoice</p>
            <h1>{invoiceNumber}</h1>
          </div>
        </div>
        <div className="invoice-print__total-due">
          <span>Total due</span>
          <strong>{formatMoney(invoice.totals.total)}</strong>
        </div>
      </header>

      <section className="invoice-print__parties" aria-label="Invoice parties">
        <AddressBlock
          title="From"
          name={identity.trading_name}
          lines={[
            identity.legal_name,
            ...addressLines(identity.address),
            `Company no. ${identity.company_number}`,
            identity.vat_number ? `VAT no. ${identity.vat_number}` : null,
          ]}
        />
        <AddressBlock
          title="Bill to"
          name={client.name}
          lines={[
            ...addressLines(client.address),
            client.vat_number ? `VAT no. ${client.vat_number}` : null,
          ]}
        />
        <dl className="invoice-print__terms">
          <Term label="Issue date" value={formatDate(invoice.issue_date)} />
          <Term label="Due date" value={formatDate(invoice.due_date)} />
          <Term label="Terms" value={`${client.terms_days} days`} />
          <Term label="Currency" value={invoice.currency} />
          {payload.locked_rate ? (
            <Term
              label="Locked rate"
              value={formatLockedRate(payload.locked_rate.rate)}
            />
          ) : null}
        </dl>
      </section>

      <table className="invoice-print__lines">
        <thead>
          <tr>
            <th scope="col">Description</th>
            <th scope="col">Qty</th>
            <th scope="col">Unit price</th>
            <th scope="col">Amount</th>
          </tr>
        </thead>
        <tbody>
          {invoice.lines.map((line) => (
            <tr key={line.id}>
              <td>{line.description}</td>
              <td>{line.qty}</td>
              <td>{formatMoney(line.unit_price)}</td>
              <td>{formatMoney(line.line_total)}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <section className="invoice-print__summary" aria-label="Invoice totals">
        <dl>
          <Term label="Subtotal" value={formatMoney(invoice.totals.subtotal)} />
          <Term
            label={
              payload.reverse_charge_note
                ? "VAT reverse charge"
                : `${formatRate(payload.vat_rate)} VAT`
            }
            value={formatMoney(invoice.totals.vat)}
          />
          <Term label="Total due" value={formatMoney(invoice.totals.total)} />
        </dl>
      </section>

      {payload.reverse_charge_note ? (
        <p className="invoice-print__legal">{payload.reverse_charge_note}</p>
      ) : null}

      <footer className="invoice-print__footer">
        <div>
          <p className="invoice-print__eyebrow">SEPA bank details</p>
          <p>{identity.bank_name}</p>
          <p>IBAN {identity.iban}</p>
          <p>BIC {identity.bic}</p>
        </div>
        <p>Generated by Ledgerly</p>
      </footer>
    </article>
  );
}

function DividendVoucherDocument({
  payload,
}: {
  payload: DividendsDocumentPayload;
}) {
  const { declaration } = payload;
  const company = requireValue(declaration.company_snapshot);
  const shareholder = requireValue(declaration.shareholder_snapshot);
  const withholding = requireValue(declaration.withholding_snapshot);

  return (
    <article
      aria-label={`Dividend voucher ${declaration.id}`}
      className="dividend-print dividend-print--voucher"
      data-ledgerly-print-ready="true"
    >
      <DividendDocumentHeader
        company={company}
        kind="Dividend voucher"
        showRegisteredOffice
      />

      <dl className="dividend-print__grid" aria-label="Dividend details">
        <DocumentFact
          label="Declaration date"
          value={formatDate(declaration.declared_date)}
        />
        <DocumentFact label="Shareholder" value={shareholder.name} />
        <DocumentFact
          label="Shareholding"
          value={formatShareholding(shareholder)}
        />
        <DocumentFact
          label="Dividend per share"
          value={formatMoney(declaration.per_share)}
        />
        <DocumentFact
          label="Total dividend"
          value={formatMoney(declaration.amount)}
        />
      </dl>

      <p className="dividend-print__note">{withholding.note}</p>

      <section className="dividend-print__body">
        <p>
          This voucher records the dividend declared by {company.legal_name} on{" "}
          {formatDate(declaration.declared_date)} to {shareholder.name}, holder
          of {formatShareholding(shareholder)}.
        </p>
      </section>

      <SignatureBlock
        label="Signed for and on behalf of the company"
        name={company.director_name}
      />
    </article>
  );
}

function BoardMinutesDocument({
  payload,
}: {
  payload: DividendsDocumentPayload;
}) {
  const { declaration } = payload;
  const company = requireValue(declaration.company_snapshot);
  const shareholder = requireValue(declaration.shareholder_snapshot);
  const headroom = requireValue(declaration.headroom_snapshot);

  return (
    <article
      aria-label={`Board minutes ${declaration.id}`}
      className="dividend-print dividend-print--minutes"
      data-ledgerly-print-ready="true"
    >
      <DividendDocumentHeader kind="Board minutes" company={company} />

      <dl className="dividend-print__grid" aria-label="Meeting details">
        <DocumentFact
          label="Meeting date"
          value={formatDate(declaration.declared_date)}
        />
        <DocumentFact
          label="Present"
          value={`${company.director_name} (Director)`}
        />
        <DocumentFact label="Share class" value={shareholder.class} />
        <DocumentFact label="Shareholder" value={shareholder.name} />
      </dl>

      <section className="dividend-print__body">
        <h2>Distributable reserves</h2>
        <p>
          The director reviewed the management accounts and confirmed the
          company had sufficient distributable reserves at declaration time.
        </p>
        <table className="dividend-print__table">
          <tbody>
            {headroom.lines.map((line) => (
              <tr key={line.label}>
                <th scope="row">{line.label}</th>
                <td>{formatMoney(line.amount)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section className="dividend-print__resolution">
        <h2>Resolution</h2>
        <p>
          It was resolved that an interim dividend of{" "}
          {formatMoney(declaration.per_share)} per {shareholder.class} share,
          totaling {formatMoney(declaration.amount)}, be declared on{" "}
          {formatDate(declaration.declared_date)} and paid by credit to the
          director&apos;s loan account.
        </p>
      </section>

      <SignatureBlock label="Chair" name={company.director_name} />
      <SignatureBlock label="Director" name={company.director_name} />
    </article>
  );
}

function ReportsPLDocument({ payload }: { payload: ReportsPLPrintPayload }) {
  const { pl } = payload;

  return (
    <article
      aria-label={`Profit and loss ${pl.period.from} to ${pl.period.to}`}
      className="reports-print"
      data-ledgerly-print-ready="true"
    >
      <header className="reports-print__header">
        <div>
          <p className="reports-print__eyebrow">Profit and loss</p>
          <h1>{payload.company_name}</h1>
        </div>
        <dl>
          <Term
            label="Period"
            value={`${formatDate(pl.period.from)} to ${formatDate(pl.period.to)}`}
          />
          <Term label="Tax year" value={pl.tax_year} />
          <Term
            label="Generated"
            value={formatDateTime(payload.generated_at)}
          />
        </dl>
      </header>

      <table className="reports-print__table">
        <thead>
          <tr>
            <th scope="col">Line</th>
            <th scope="col">Amount</th>
          </tr>
        </thead>
        <tbody>
          {pl.income.map((line) => (
            <tr key={`${line.client_id || line.label}-${line.currency}`}>
              <th scope="row">{line.label}</th>
              <td>{formatReportsMoney(line.amount)}</td>
            </tr>
          ))}
          <tr>
            <th scope="row">{pl.realised_fx_gains.label}</th>
            <td>{formatReportsMoney(pl.realised_fx_gains.amount)}</td>
          </tr>
          <tr className="reports-print__total">
            <th scope="row">Turnover</th>
            <td>{formatReportsMoney(pl.income_total)}</td>
          </tr>
          {pl.expenses.map((line) => (
            <tr key={line.account_code}>
              <th scope="row">{line.account_name}</th>
              <td>{formatReportsMoney(line.amount)}</td>
            </tr>
          ))}
          <tr className="reports-print__total">
            <th scope="row">Expenses</th>
            <td>{formatReportsMoney(pl.expense_total)}</td>
          </tr>
          <tr className="reports-print__total">
            <th scope="row">Profit before tax</th>
            <td>{formatReportsMoney(pl.profit_before_tax)}</td>
          </tr>
          <tr>
            <th scope="row">{pl.corporate_tax.label}</th>
            <td>{formatReportsMoney(pl.corporate_tax.amount)}</td>
          </tr>
          <tr className="reports-print__grand-total">
            <th scope="row">Net profit for the period</th>
            <td>{formatReportsMoney(pl.net_profit)}</td>
          </tr>
        </tbody>
      </table>

      <footer className="reports-print__footer">
        <span>Generated by Ledgerly</span>
        {payload.app_version ? <span>{payload.app_version}</span> : null}
      </footer>
    </article>
  );
}

function DividendDocumentHeader({
  kind,
  company,
  showRegisteredOffice = false,
}: {
  kind: string;
  company: DividendCompanySnapshot;
  showRegisteredOffice?: boolean;
}) {
  const logoSrc = company.logo_data_uri ?? company.logo_asset_url ?? null;
  const heading = (
    <>
      <p className="dividend-print__eyebrow">{kind}</p>
      <h1>{company.legal_name}</h1>
      {company.trading_name !== company.legal_name ? (
        <p>Trading as {company.trading_name}</p>
      ) : null}
      <p>Company no. {company.company_number}</p>
      {showRegisteredOffice ? (
        <p>
          Registered office: {addressLines(company.registered_office).join(", ")}
        </p>
      ) : null}
    </>
  );

  return (
    <header className="dividend-print__header">
      {logoSrc ? (
        <div className="dividend-print__brand">
          <img alt="" aria-hidden="true" src={logoSrc} />
          <div>{heading}</div>
        </div>
      ) : (
        heading
      )}
    </header>
  );
}

function DocumentFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="dividend-print__fact">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

function SignatureBlock({ label, name }: { label: string; name: string }) {
  return (
    <section className="dividend-print__signature">
      <div aria-hidden="true" />
      <p>{label}</p>
      <p>{name}</p>
    </section>
  );
}

function AddressBlock({
  lines,
  name,
  title,
}: {
  lines: Array<string | null>;
  name: string;
  title: string;
}) {
  return (
    <section className="invoice-print__address">
      <p className="invoice-print__eyebrow">{title}</p>
      <h2>{name}</h2>
      {lines.filter(Boolean).map((line) => (
        <p key={line}>{line}</p>
      ))}
    </section>
  );
}

function Term({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

function printRoute(pathname: string): PrintRoute {
  const invoiceMatch = pathname.match(/^\/print\/invoice\/([^/]+)$/);
  if (invoiceMatch) {
    return { id: decodeURIComponent(invoiceMatch[1]), kind: "invoice" };
  }
  const voucherMatch = pathname.match(/^\/print\/dividend-voucher\/([^/]+)$/);
  if (voucherMatch) {
    return {
      id: decodeURIComponent(voucherMatch[1]),
      kind: "dividend-voucher",
    };
  }
  const minutesMatch = pathname.match(/^\/print\/board-minutes\/([^/]+)$/);
  if (minutesMatch) {
    return { id: decodeURIComponent(minutesMatch[1]), kind: "board-minutes" };
  }
  const reportsPLMatch = pathname.match(/^\/print\/reports\/pl\/([^/]+)$/);
  if (reportsPLMatch) {
    return { id: decodeURIComponent(reportsPLMatch[1]), kind: "reports-pl" };
  }
  return { id: "", kind: "unknown" };
}

function fetchPrintPayload(
  route: PrintRoute,
  draft: boolean,
): Promise<PrintPayload> {
  if (route.kind === "invoice") {
    return getInvoicePrintPayload(route.id, draft) as Promise<PrintPayload>;
  }
  if (route.kind === "dividend-voucher" || route.kind === "board-minutes") {
    return getDividendDocumentPayload(route.id) as Promise<PrintPayload>;
  }
  if (route.kind === "reports-pl") {
    return getReportsPLPrintPayload(route.id) as Promise<PrintPayload>;
  }
  return Promise.reject(new Error("unknown print route"));
}

function readStoredPayload(route: PrintRoute) {
  if (typeof window === "undefined") {
    return null;
  }
  const key = printStorageKey(route);
  if (!key) {
    return null;
  }
  const raw = window.localStorage.getItem(key);
  if (!raw) {
    return null;
  }
  try {
    return JSON.parse(raw) as PrintPayload;
  } catch {
    return null;
  }
}

function printStorageKey(route: PrintRoute) {
  if (route.kind === "invoice") {
    return `${invoicePrintStoragePrefix}${route.id}`;
  }
  if (route.kind === "dividend-voucher" || route.kind === "board-minutes") {
    return `${dividendPrintStoragePrefix}${route.kind}.${route.id}`;
  }
  if (route.kind === "reports-pl") {
    return `${reportsPLPrintStoragePrefix}${route.id}`;
  }
  return "";
}

function addressLines(address: {
  country: string;
  line1: string;
  line2: string;
  locality: string;
  postal_code: string;
  region: string;
}) {
  return [
    address.line1,
    address.line2,
    address.locality,
    address.region,
    address.postal_code,
    address.country,
  ].filter((line) => line.trim() !== "");
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "2-digit",
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  }).format(new Date(value));
}

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    month: "short",
    timeZone: "UTC",
    year: "numeric",
  }).format(new Date(value));
}

function formatMoney(value: { amount: number; currency: string }) {
  return new Intl.NumberFormat("en-IE", {
    currency: value.currency,
    style: "currency",
  }).format(value.amount / 100);
}

function formatReportsMoney(value: { amount_minor: number; currency: string }) {
  return new Intl.NumberFormat("en-IE", {
    currency: value.currency,
    style: "currency",
  }).format(value.amount_minor / 100);
}

function formatRate(value: string) {
  const rate = Number.parseFloat(value);
  if (!Number.isFinite(rate)) {
    return value;
  }
  return new Intl.NumberFormat("en-GB", {
    maximumFractionDigits: 2,
    style: "percent",
  }).format(rate);
}

function formatLockedRate(value: string) {
  const rate = Number.parseFloat(value);
  if (!Number.isFinite(rate)) {
    return value;
  }
  return new Intl.NumberFormat("en-GB", {
    maximumFractionDigits: 4,
    minimumFractionDigits: 4,
  }).format(rate);
}

function initials(value: string) {
  return value
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() ?? "")
    .join("");
}

function formatShareholding(shareholder: { class: string; shares: number }) {
  return `${shareholder.shares} ${shareholder.class}`;
}

function requireValue<T>(value: T | null | undefined): T {
  if (value === null || value === undefined) {
    throw new Error("Missing dividend document snapshot");
  }
  return value;
}
