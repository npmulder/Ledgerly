import {
  type ChangeEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "react-router-dom";

import { isApiError } from "@/api/client";
import {
  getInvoicingClients,
  getInvoice,
  invoicePDFPath,
  patchInvoice,
  revertInvoice,
  sendInvoiceReminder,
  sendInvoice,
  type InvoicingClient,
  type InvoicingInvoice,
  type InvoicingInvoicePatch,
  type InvoicingSendInvoiceResult,
} from "@/api/invoicing";
import { queryKeys } from "@/api/queryKeys";
import {
  AuditHistoryPanel,
  Badge,
  Button,
  Card,
  Field,
  Input,
  PageTitle,
  Select,
  formatMinorUnits,
} from "@/components";

type Currency = InvoicingInvoice["currency"];
type VATTreatment = InvoicingInvoice["vat_treatment"];
type InvoiceStatus = InvoicingInvoice["status"];
type LockedRate = InvoicingSendInvoiceResult["locked_rate"];

type LineForm = {
  description: string;
  id: string;
  qty: string;
  unitPrice: string;
};

type InvoiceForm = {
  clientId: string;
  currency: Currency;
  dueDate: string;
  issueDate: string;
  lines: LineForm[];
  vatRegistered: boolean;
  vatTreatment: VATTreatment;
};

type LocalTotals = {
  subtotal: number;
  total: number;
  vat: number;
};

type AutosaveState =
  | { status: "saved"; updatedAt: string | null }
  | { status: "saving"; updatedAt: string | null }
  | { message: string; status: "error"; updatedAt: string | null };

const domesticVATRate = 0.2;
const reverseChargeWording =
  "VAT reverse charge applies: VAT to be accounted for by the recipient under Article 196, Council Directive 2006/112/EC. Supplier is established in the Isle of Man.";

export function InvoiceEditorScreen() {
  const params = useParams();
  const invoiceId = params.id ?? "";
  const clientsQuery = useQuery({
    queryFn: () => getInvoicingClients(false),
    queryKey: queryKeys.invoicing.clients(false),
  });
  const invoiceQuery = useQuery({
    enabled: invoiceId.trim() !== "",
    queryFn: () => getInvoice(invoiceId),
    queryKey: queryKeys.invoicing.invoice(invoiceId),
  });

  if (invoiceQuery.isPending || clientsQuery.isPending) {
    return (
      <div className="invoice-editor-screen">
        <PageTitle>Invoice editor</PageTitle>
        <Card title="Loading">
          <p className="type-secondary">Loading invoice.</p>
        </Card>
      </div>
    );
  }

  if (invoiceQuery.isError || clientsQuery.isError) {
    return (
      <div className="invoice-editor-screen">
        <PageTitle>Invoice editor</PageTitle>
        <Card title="Unable to load invoice">
          <ProblemAlert
            error={invoiceQuery.error ?? clientsQuery.error}
            fallbackTitle="Unable to load invoice"
          />
        </Card>
      </div>
    );
  }

  if (!invoiceQuery.data || !clientsQuery.data) {
    return (
      <div className="invoice-editor-screen">
        <PageTitle>Invoice editor</PageTitle>
        <Card title="Loading">
          <p className="type-secondary">Loading invoice.</p>
        </Card>
      </div>
    );
  }

  return (
    <LoadedInvoiceEditor
      clients={clientsQuery.data.clients}
      initialInvoice={invoiceQuery.data}
      invoiceId={invoiceId}
      key={invoiceQuery.data.id}
    />
  );
}

function LoadedInvoiceEditor({
  clients,
  initialInvoice,
  invoiceId,
}: {
  clients: InvoicingClient[];
  initialInvoice: InvoicingInvoice;
  invoiceId: string;
}) {
  const queryClient = useQueryClient();
  const lineCounterRef = useRef(0);
  const [invoice, setInvoice] = useState<InvoicingInvoice>(initialInvoice);
  const [form, setForm] = useState<InvoiceForm>(() =>
    invoiceToForm(initialInvoice),
  );
  const [lockedRate, setLockedRate] = useState<LockedRate | null>(null);
  const [reminderStatus, setReminderStatus] = useState<{
    kind: "error" | "success";
    text: string;
  } | null>(null);
  const [validationSummary, setValidationSummary] = useState<string[]>([]);

  const autosave = useDraftAutosave(invoiceId, (updated) => {
    setInvoice(updated);
    queryClient.setQueryData(queryKeys.invoicing.invoice(updated.id), updated);
    queryClient.invalidateQueries({
      queryKey: queryKeys.audit.history("invoicing", "invoice", updated.id),
    });
  });

  const sendMutation = useMutation({
    mutationFn: () => sendInvoice(invoiceId),
    onSuccess: (result) => {
      setInvoice(result.invoice);
      setForm(invoiceToForm(result.invoice));
      setLockedRate(result.locked_rate);
      setValidationSummary([]);
      queryClient.setQueryData(
        queryKeys.invoicing.invoice(result.invoice.id),
        result.invoice,
      );
      queryClient.invalidateQueries({
        queryKey: queryKeys.invoicing.invoices(),
      });
    },
  });

  const revertMutation = useMutation({
    mutationFn: () => revertInvoice(invoiceId),
    onSuccess: (updated) => {
      setInvoice(updated);
      setForm(invoiceToForm(updated));
      setLockedRate(null);
      setValidationSummary([]);
      queryClient.setQueryData(
        queryKeys.invoicing.invoice(updated.id),
        updated,
      );
      queryClient.invalidateQueries({
        queryKey: queryKeys.invoicing.invoices(),
      });
    },
  });

  const reminderMutation = useMutation({
    mutationFn: () => sendInvoiceReminder(invoiceId),
    onError: (error) => {
      setReminderStatus({
        kind: "error",
        text: problemMessage(error, "Unable to send reminder"),
      });
    },
    onSuccess: (result) => {
      setInvoice(result.invoice);
      setReminderStatus({
        kind: "success",
        text: `Reminder sent for ${result.invoice.number ?? "invoice"}.`,
      });
      queryClient.setQueryData(
        queryKeys.invoicing.invoice(result.invoice.id),
        result.invoice,
      );
      queryClient.invalidateQueries({
        queryKey: queryKeys.invoicing.invoices(),
      });
    },
  });

  const selectedClient = clients.find((client) => client.id === form.clientId);
  const isDraft = invoice.status === "draft";
  const isReadOnly = !isDraft;
  const totals = useMemo(() => calculateTotals(form), [form]);
  const invoiceNumber = invoice.number ?? "Draft";

  function applyForm(next: InvoiceForm) {
    if (!isDraft) {
      return;
    }
    setForm(next);
    setValidationSummary([]);
    autosave.schedule(patchFromForm(next));
  }

  function handleClientChange(event: ChangeEvent<HTMLSelectElement>) {
    const client = clients.find((item) => item.id === event.target.value);
    if (!client) {
      return;
    }
    const nextCurrency = client.default_currency;
    const next: InvoiceForm = {
      ...form,
      clientId: client.id,
      currency: nextCurrency,
      dueDate: addDays(form.issueDate, client.terms_days),
      lines: form.lines.map((line) => ({ ...line })),
      vatRegistered: form.vatRegistered,
      vatTreatment: client.vat_treatment,
    };
    applyForm(next);
  }

  function handleFieldChange<Key extends keyof InvoiceForm>(
    key: Key,
    value: InvoiceForm[Key],
  ) {
    applyForm({ ...form, [key]: value });
  }

  function handleCurrencyChange(currency: Currency) {
    applyForm({ ...form, currency });
  }

  function updateLine(index: number, patch: Partial<LineForm>) {
    applyForm({
      ...form,
      lines: form.lines.map((line, lineIndex) =>
        lineIndex === index ? { ...line, ...patch } : line,
      ),
    });
  }

  function addLine() {
    lineCounterRef.current += 1;
    applyForm({
      ...form,
      lines: [
        ...form.lines,
        {
          description: "",
          id: `line_${Date.now()}_${lineCounterRef.current}`,
          qty: "1",
          unitPrice: "",
        },
      ],
    });
  }

  function removeLine(index: number) {
    applyForm({
      ...form,
      lines: form.lines.filter((_, lineIndex) => lineIndex !== index),
    });
  }

  function moveLine(index: number, direction: -1 | 1) {
    const target = index + direction;
    if (target < 0 || target >= form.lines.length) {
      return;
    }
    const lines = [...form.lines];
    [lines[index], lines[target]] = [lines[target], lines[index]];
    applyForm({ ...form, lines });
  }

  async function handleSend() {
    const issues = completionIssues(form, clients);
    if (issues.length > 0) {
      setValidationSummary(issues);
      return;
    }
    try {
      await autosave.flush();
      await sendMutation.mutateAsync();
    } catch (error) {
      setValidationSummary([problemMessage(error, "Unable to send invoice")]);
    }
  }

  async function handleRevert() {
    try {
      await revertMutation.mutateAsync();
    } catch (error) {
      setValidationSummary([problemMessage(error, "Unable to revert invoice")]);
    }
  }

  return (
    <div className="invoice-editor-screen">
      <div className="invoice-editor-header">
        <PageTitle>Invoice editor</PageTitle>
        <div className="invoice-editor-header__meta">
          <Badge variant={badgeForStatus(invoice.status)}>
            {statusLabel(invoice.status)}
          </Badge>
          <span>{invoiceNumber}</span>
          <AutosaveChip readOnly={isReadOnly} state={autosave.state} />
        </div>
      </div>

      <div className="invoice-editor-layout">
        <div className="invoice-editor-main">
          {validationSummary.length > 0 && (
            <div className="invoice-validation-summary" role="alert">
              <strong>Complete invoice before sending</strong>
              <ul>
                {validationSummary.map((issue) => (
                  <li key={issue}>{issue}</li>
                ))}
              </ul>
            </div>
          )}
          {reminderStatus && (
            <div
              className={`invoice-toast invoice-toast--${reminderStatus.kind}`}
              role={reminderStatus.kind === "success" ? "status" : "alert"}
            >
              {reminderStatus.text}
            </div>
          )}

          <Card
            actions={
              isDraft ? (
                <Button
                  disabled={sendMutation.isPending}
                  onClick={handleSend}
                  variant="primary"
                >
                  {sendMutation.isPending ? "Sending" : "Send invoice"}
                </Button>
              ) : (
                <div className="invoice-editor-actions">
                  {invoice.number && <strong>{invoice.number}</strong>}
                  {invoice.sent_at && invoice.status !== "paid" && (
                    <Button
                      disabled={revertMutation.isPending}
                      onClick={handleRevert}
                      size="small"
                      variant="secondary"
                    >
                      {revertMutation.isPending
                        ? "Reverting"
                        : "Revert same-day"}
                    </Button>
                  )}
                </div>
              )
            }
            title="Invoice details"
          >
            <div className="invoice-field-grid">
              <Field
                helperText="Only active clients are available."
                label="Client"
              >
                <Select
                  aria-label="Client"
                  disabled={isReadOnly}
                  onChange={handleClientChange}
                  value={form.clientId}
                >
                  {clients.map((client) => (
                    <option key={client.id} value={client.id}>
                      {client.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label="Issue date">
                <Input
                  aria-label="Issue date"
                  locked={isReadOnly}
                  onChange={(event) =>
                    handleFieldChange("issueDate", event.target.value)
                  }
                  type="date"
                  value={form.issueDate}
                />
              </Field>
              <Field label="Due date">
                <Input
                  aria-label="Due date"
                  locked={isReadOnly}
                  onChange={(event) =>
                    handleFieldChange("dueDate", event.target.value)
                  }
                  type="date"
                  value={form.dueDate}
                />
              </Field>
              <Field
                helperText={
                  isReadOnly
                    ? "Locked after send."
                    : selectedClient
                      ? `Default from ${selectedClient.name}, editable while draft.`
                      : "Editable while draft."
                }
                label="Currency"
              >
                <Select
                  aria-label="Currency"
                  disabled={isReadOnly}
                  onChange={(event) =>
                    handleCurrencyChange(event.target.value as Currency)
                  }
                  value={form.currency}
                >
                  <option value="EUR">EUR</option>
                  <option value="GBP">GBP</option>
                </Select>
              </Field>
              <Field label="VAT treatment">
                <Select
                  aria-label="VAT treatment"
                  disabled={isReadOnly}
                  onChange={(event) =>
                    handleFieldChange(
                      "vatTreatment",
                      event.target.value as VATTreatment,
                    )
                  }
                  value={form.vatTreatment}
                >
                  <option value="domestic">Domestic VAT</option>
                  <option value="reverse-charge-eu-b2b">
                    Reverse charge EU B2B
                  </option>
                </Select>
              </Field>
              <Field label="Locked FX rate">
                <FXRateField
                  currency={form.currency}
                  invoice={invoice}
                  isDraft={isDraft}
                  lockedRate={lockedRate}
                />
              </Field>
            </div>
          </Card>

          <Card
            actions={
              isDraft ? (
                <Button onClick={addLine} size="small" variant="secondary">
                  Add line
                </Button>
              ) : (
                <span className="invoice-locked-note">Locked</span>
              )
            }
            title="Line items"
          >
            <div className="invoice-lines" role="table" aria-label="Line items">
              <div className="invoice-lines__header" role="row">
                <span>Description</span>
                <span>Qty</span>
                <span>Unit price</span>
                <span>Total</span>
                <span>Actions</span>
              </div>
              {form.lines.map((line, index) => {
                const lineTotal = lineTotalMinor(line);
                return (
                  <div className="invoice-line-row" key={line.id} role="row">
                    <Input
                      aria-label={`Description line ${index + 1}`}
                      locked={isReadOnly}
                      onChange={(event) =>
                        updateLine(index, { description: event.target.value })
                      }
                      value={line.description}
                    />
                    <Input
                      aria-label={`Quantity line ${index + 1}`}
                      inputMode="decimal"
                      locked={isReadOnly}
                      onChange={(event) =>
                        updateLine(index, { qty: event.target.value })
                      }
                      value={line.qty}
                    />
                    <Input
                      aria-label={`Unit price line ${index + 1}`}
                      inputMode="decimal"
                      locked={isReadOnly}
                      onChange={(event) =>
                        updateLine(index, { unitPrice: event.target.value })
                      }
                      value={line.unitPrice}
                    />
                    <span className="invoice-line-row__total">
                      {formatMoneyAmount(lineTotal, form.currency)}
                    </span>
                    <div className="invoice-line-row__actions">
                      {isDraft ? (
                        <>
                          <Button
                            aria-label={`Move line ${index + 1} up`}
                            disabled={index === 0}
                            onClick={() => moveLine(index, -1)}
                            size="small"
                            variant="secondary"
                          >
                            ↑
                          </Button>
                          <Button
                            aria-label={`Move line ${index + 1} down`}
                            disabled={index === form.lines.length - 1}
                            onClick={() => moveLine(index, 1)}
                            size="small"
                            variant="secondary"
                          >
                            ↓
                          </Button>
                          <Button
                            aria-label={`Remove line ${index + 1}`}
                            onClick={() => removeLine(index)}
                            size="small"
                            variant="danger"
                          >
                            Remove
                          </Button>
                        </>
                      ) : (
                        <span className="invoice-locked-note">Locked</span>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </Card>
        </div>

        <aside className="invoice-editor-rail">
          {totals && (
            <TotalsCard
              currency={form.currency}
              invoice={invoice}
              lockedRate={lockedRate}
              totals={totals}
            />
          )}
          <DocumentPreview invoice={invoice} />
          <AuditHistoryPanel
            entity="invoice"
            entityId={invoice.id}
            module="invoicing"
          />
          <ReminderCard
            invoice={invoice}
            onSend={() => reminderMutation.mutate()}
            pending={reminderMutation.isPending}
          />
          <AdvisorNotes
            currency={form.currency}
            isReverseCharge={form.vatTreatment === "reverse-charge-eu-b2b"}
          />
        </aside>
      </div>
    </div>
  );
}

function useDraftAutosave(
  invoiceId: string,
  onSaved: (invoice: InvoicingInvoice) => void,
) {
  const onSavedRef = useRef(onSaved);
  const timerRef = useRef<number | null>(null);
  const pendingPatchRef = useRef<InvoicingInvoicePatch | null>(null);
  const inFlightRef = useRef<Promise<InvoicingInvoice | null> | null>(null);
  const [state, setState] = useState<AutosaveState>({
    status: "saved",
    updatedAt: null,
  });

  useEffect(() => {
    onSavedRef.current = onSaved;
  }, [onSaved]);

  const saveNow = useCallback(
    (patch: InvoicingInvoicePatch) => {
      setState((current) => ({
        status: "saving",
        updatedAt: current.updatedAt,
      }));
      const request = patchInvoice(invoiceId, patch)
        .then((updated) => {
          onSavedRef.current(updated);
          setState({ status: "saved", updatedAt: updated.updated_at });
          return updated;
        })
        .catch((error: unknown) => {
          setState({
            message: problemMessage(error, "Autosave failed"),
            status: "error",
            updatedAt: null,
          });
          throw error;
        });
      inFlightRef.current = request;
      void request
        .finally(() => {
          if (inFlightRef.current === request) {
            inFlightRef.current = null;
          }
        })
        .catch(() => undefined);
      return request;
    },
    [invoiceId],
  );

  const flush = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    const pending = pendingPatchRef.current;
    pendingPatchRef.current = null;
    if (pending) {
      return saveNow(pending).catch(() => null);
    }
    return inFlightRef.current ?? Promise.resolve(null);
  }, [saveNow]);

  const schedule = useCallback(
    (patch: InvoicingInvoicePatch) => {
      pendingPatchRef.current = patch;
      setState((current) => ({
        status: "saving",
        updatedAt: current.updatedAt,
      }));
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current);
      }
      timerRef.current = window.setTimeout(() => {
        timerRef.current = null;
        const pending = pendingPatchRef.current;
        pendingPatchRef.current = null;
        if (pending) {
          void saveNow(pending).catch(() => undefined);
        }
      }, 800);
    },
    [saveNow],
  );

  useEffect(() => {
    const handleBeforeUnload = () => {
      void flush();
    };
    window.addEventListener("beforeunload", handleBeforeUnload);
    return () => {
      window.removeEventListener("beforeunload", handleBeforeUnload);
      void flush();
    };
  }, [flush]);

  return { flush, schedule, state };
}

function AutosaveChip({
  readOnly,
  state,
}: {
  readOnly: boolean;
  state: AutosaveState;
}) {
  if (readOnly) {
    return (
      <span className="autosave-chip autosave-chip--readonly" role="status">
        Read-only
      </span>
    );
  }
  if (state.status === "saving") {
    return (
      <span className="autosave-chip autosave-chip--saving" role="status">
        Saving
      </span>
    );
  }
  if (state.status === "error") {
    return (
      <span className="autosave-chip autosave-chip--error" role="status">
        Error
      </span>
    );
  }
  return (
    <span className="autosave-chip autosave-chip--saved" role="status">
      {state.updatedAt ? `Saved ${timeLabel(state.updatedAt)}` : "Saved"}
    </span>
  );
}

function FXRateField({
  currency,
  invoice,
  isDraft,
  lockedRate,
}: {
  currency: Currency;
  invoice: InvoicingInvoice;
  isDraft: boolean;
  lockedRate: LockedRate | null;
}) {
  const draftRate = invoice.totals.approx_gbp?.rate;
  if (isDraft) {
    const rate = draftRate?.value ?? (currency === "GBP" ? "1" : "pending");
    const source = draftRate
      ? `${draftRate.source} ${dateLabel(draftRate.rate_date)}`
      : "TodayRate";
    return (
      <div className="invoice-fx-field" aria-label="Locked FX rate">
        <strong>≈ {rate}</strong>
        <span>{source}, locks at send</span>
      </div>
    );
  }

  const source = lockedRate
    ? `${lockedRate.source} ${dateLabel(lockedRate.rate_date)}`
    : invoice.lock_id
      ? `ECB ${dateLabel(invoice.sent_at ?? invoice.updated_at)}`
      : "No FX lock";
  const rate = lockedRate?.rate ?? "locked";
  return (
    <div
      className="invoice-fx-field invoice-fx-field--locked"
      aria-label="Locked FX rate"
    >
      <strong>🔒 {rate}</strong>
      <span>Source: {source}</span>
    </div>
  );
}

function TotalsCard({
  currency,
  invoice,
  lockedRate,
  totals,
}: {
  currency: Currency;
  invoice: InvoicingInvoice;
  lockedRate: LockedRate | null;
  totals: LocalTotals;
}) {
  const rateValue = lockedRate?.rate ?? invoice.totals.approx_gbp?.rate.value;
  const approxGBP =
    currency === "GBP"
      ? totals.total
      : rateValue
        ? Math.round(totals.total * parseRate(rateValue))
        : invoice.totals.approx_gbp?.amount.amount;

  return (
    <Card title="Totals">
      <dl className="invoice-totals">
        <div>
          <dt>Subtotal</dt>
          <dd>{formatMoneyAmount(totals.subtotal, currency)}</dd>
        </div>
        <div>
          <dt>VAT</dt>
          <dd>{formatMoneyAmount(totals.vat, currency)}</dd>
        </div>
        <div className="invoice-totals__total">
          <dt>Total</dt>
          <dd>{formatMoneyAmount(totals.total, currency)}</dd>
        </div>
      </dl>
      <p className="invoice-gbp-note">
        ≈GBP{" "}
        {approxGBP === undefined
          ? "pending rate"
          : formatMoneyAmount(approxGBP, "GBP")}
        {invoice.status === "draft" ? " indicative until send" : " locked"}
      </p>
    </Card>
  );
}

function DocumentPreview({ invoice }: { invoice: InvoicingInvoice }) {
  return (
    <Card title="Document preview">
      <a className="invoice-document-preview" href={invoicePDFPath(invoice.id)}>
        <span className="invoice-document-preview__page">
          {invoice.number ?? "Draft"}
        </span>
        <span>
          {invoice.pdf_asset
            ? "Open invoice PDF"
            : "PDF placeholder until INV-8"}
        </span>
      </a>
    </Card>
  );
}

function ReminderCard({
  invoice,
  onSend,
  pending,
}: {
  invoice: InvoicingInvoice;
  onSend: () => void;
  pending: boolean;
}) {
  const reminders = invoice.reminders ?? [];
  const canSend = invoice.status === "overdue";
  return (
    <Card
      actions={
        canSend ? (
          <Button disabled={pending} onClick={onSend} size="small">
            {pending ? "Sending" : "Send reminder"}
          </Button>
        ) : null
      }
      title="Reminders"
    >
      {reminders.length === 0 ? (
        <p className="type-secondary">No reminders sent.</p>
      ) : (
        <ul className="invoice-reminder-log">
          {reminders.map((reminder) => (
            <li key={reminder.sent_at}>
              <span>Reminder sent</span>
              <time dateTime={reminder.sent_at}>
                {formatReminderDateTime(reminder.sent_at)}
              </time>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function AdvisorNotes({
  currency,
  isReverseCharge,
}: {
  currency: Currency;
  isReverseCharge: boolean;
}) {
  return (
    <Card title="Advisor notes">
      <div className="invoice-advisor-notes">
        {isReverseCharge && <p>{reverseChargeWording}</p>}
        <p>
          FX gain or loss is measured when the invoice is settled against the
          locked GBP value from send time.
        </p>
        {currency === "GBP" && (
          <p>
            GBP invoices lock an identity rate and do not create FX movement.
          </p>
        )}
      </div>
    </Card>
  );
}

function ProblemAlert({
  error,
  fallbackTitle,
}: {
  error: unknown;
  fallbackTitle: string;
}) {
  return (
    <div className="problem-alert" role="alert">
      <strong>{problemMessage(error, fallbackTitle)}</strong>
      {isApiError(error) && error.problem.detail && (
        <span>{error.problem.detail}</span>
      )}
    </div>
  );
}

function invoiceToForm(invoice: InvoicingInvoice): InvoiceForm {
  return {
    clientId: invoice.client_id,
    currency: invoice.currency,
    dueDate: dateInputValue(invoice.due_date),
    issueDate: dateInputValue(invoice.issue_date),
    lines: invoice.lines.map((line) => ({
      description: line.description,
      id: line.id,
      qty: line.qty,
      unitPrice: decimalInputValue(line.unit_price.amount),
    })),
    vatRegistered: invoice.vat_registered,
    vatTreatment: invoice.vat_treatment,
  };
}

function patchFromForm(form: InvoiceForm): InvoicingInvoicePatch {
  return {
    client_id: form.clientId,
    currency: form.currency,
    due_date: form.dueDate,
    issue_date: form.issueDate,
    lines: form.lines.map((line) => ({
      description: line.description.trim(),
      id: line.id,
      qty: normalizeQuantity(line.qty),
      unit_price: {
        amount: decimalToMinor(line.unitPrice),
        currency: form.currency,
      },
    })),
    vat_treatment: form.vatTreatment,
  };
}

function completionIssues(form: InvoiceForm, clients: InvoicingClient[]) {
  const issues: string[] = [];
  if (!clients.some((client) => client.id === form.clientId)) {
    issues.push("Select an active client.");
  }
  if (!form.issueDate) {
    issues.push("Set an issue date.");
  }
  if (!form.dueDate) {
    issues.push("Set a due date.");
  }
  if (form.issueDate && form.dueDate && form.dueDate < form.issueDate) {
    issues.push("Due date must be on or after issue date.");
  }
  if (form.lines.length === 0) {
    issues.push("Add at least one invoice line.");
  }
  form.lines.forEach((line, index) => {
    if (!line.description.trim()) {
      issues.push(`Line ${index + 1} needs a description.`);
    }
    if (decimalNumber(line.qty) <= 0) {
      issues.push(`Line ${index + 1} needs a positive quantity.`);
    }
    if (decimalToMinor(line.unitPrice) <= 0) {
      issues.push(`Line ${index + 1} needs a positive unit price.`);
    }
  });
  return issues;
}

function calculateTotals(form: InvoiceForm): LocalTotals {
  const subtotal = form.lines.reduce(
    (sum, line) => sum + lineTotalMinor(line),
    0,
  );
  const vat =
    form.vatTreatment === "domestic" && form.vatRegistered
      ? Math.round(subtotal * domesticVATRate)
      : 0;
  return { subtotal, total: subtotal + vat, vat };
}

function lineTotalMinor(line: LineForm) {
  return Math.round(decimalNumber(line.qty) * decimalToMinor(line.unitPrice));
}

function decimalToMinor(value: string) {
  const parsed = decimalNumber(value);
  if (!Number.isFinite(parsed)) {
    return 0;
  }
  return Math.round(parsed * 100);
}

function decimalNumber(value: string) {
  const parsed = Number.parseFloat(value.replace(",", "."));
  return Number.isFinite(parsed) ? parsed : 0;
}

function normalizeQuantity(value: string) {
  const parsed = decimalNumber(value);
  return parsed > 0 ? String(parsed) : value.trim();
}

function decimalInputValue(amountMinor: number) {
  return (amountMinor / 100).toFixed(2);
}

function dateInputValue(value: string) {
  return value.slice(0, 10);
}

function addDays(date: string, days: number) {
  const parsed = new Date(`${date}T00:00:00Z`);
  parsed.setUTCDate(parsed.getUTCDate() + days);
  return parsed.toISOString().slice(0, 10);
}

function formatMoneyAmount(amountMinor: number, currency: Currency | "GBP") {
  return formatMinorUnits({ amountMinor, currency });
}

function parseRate(value: string) {
  const trimmed = value.trim();
  if (trimmed.includes("/")) {
    const [numerator, denominator] = trimmed.split("/");
    const top = Number(numerator);
    const bottom = Number(denominator);
    return bottom ? top / bottom : 0;
  }
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : 0;
}

function badgeForStatus(status: InvoiceStatus) {
  if (status === "overdue") {
    return "overdue";
  }
  if (status === "paid") {
    return "paid";
  }
  if (status === "sent") {
    return "sent";
  }
  return "draft";
}

function statusLabel(status: InvoiceStatus) {
  return status === "overdue" ? "sent" : status;
}

function dateLabel(value: string) {
  return value.slice(0, 10);
}

function formatReminderDateTime(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

function timeLabel(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function problemMessage(error: unknown, fallbackTitle: string) {
  if (isApiError(error)) {
    return error.problem.detail
      ? `${error.problem.title}: ${error.problem.detail}`
      : error.problem.title;
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallbackTitle;
}
