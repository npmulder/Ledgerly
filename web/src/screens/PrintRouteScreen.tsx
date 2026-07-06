import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useLocation } from "react-router-dom";

import {
  getInvoicePrintPayload,
  type InvoicingInvoicePrintPayload,
} from "@/api/invoicing";

const printStoragePrefix = "ledgerly.print.invoice.";

export function PrintRouteScreen() {
  const location = useLocation();
  const route = useMemo(() => invoicePrintRoute(location.pathname), [location]);
  const draft = useMemo(
    () => new URLSearchParams(location.search).get("draft") === "1",
    [location.search],
  );
  const [storedPayload] = useState(() =>
    route.invoiceID ? readStoredPayload(route.invoiceID) : null,
  );

  const payloadQuery = useQuery({
    enabled: Boolean(route.invoiceID && !storedPayload),
    queryFn: () => getInvoicePrintPayload(route.invoiceID, draft),
    queryKey: ["invoice-print", route.invoiceID, draft],
    retry: false,
  });

  const payload = storedPayload ?? payloadQuery.data ?? null;

  if (!route.invoiceID) {
    return (
      <main className="print-route print-route--state">
        <p>Print route not found</p>
      </main>
    );
  }

  if (!payload) {
    return (
      <main className="print-route print-route--state">
        <p>{payloadQuery.isError ? "Unable to load invoice" : "Loading"}</p>
      </main>
    );
  }

  return (
    <main className="print-route">
      <InvoiceDocument payload={payload} />
    </main>
  );
}

function InvoiceDocument({ payload }: { payload: InvoicingInvoicePrintPayload }) {
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
            <Term label="Locked rate" value={payload.locked_rate.rate} />
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

function invoicePrintRoute(pathname: string) {
  const match = pathname.match(/^\/print\/invoice\/([^/]+)$/);
  return { invoiceID: match ? decodeURIComponent(match[1]) : "" };
}

function readStoredPayload(id: string) {
  if (typeof window === "undefined") {
    return null;
  }
  const key = `${printStoragePrefix}${id}`;
  const raw = window.localStorage.getItem(key);
  if (!raw) {
    return null;
  }
  try {
    return JSON.parse(raw) as InvoicingInvoicePrintPayload;
  } catch {
    return null;
  }
}

function addressLines(address: InvoicingInvoicePrintPayload["identity"]["address"]) {
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

function formatMoney(value: { amount: number; currency: "EUR" | "GBP" }) {
  return new Intl.NumberFormat("en-IE", {
    currency: value.currency,
    style: "currency",
  }).format(value.amount / 100);
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

function initials(value: string) {
  return value
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part[0]?.toUpperCase() ?? "")
    .join("");
}
