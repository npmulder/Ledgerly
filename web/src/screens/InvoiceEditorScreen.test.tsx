import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type {
  InvoicingClient,
  InvoicingInvoice,
  InvoicingInvoicePatch,
  InvoicingSendInvoiceResult,
} from "@/api/invoicing";
import { InvoiceEditorScreen } from "@/screens/InvoiceEditorScreen";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("InvoiceEditorScreen", () => {
  it("debounces autosave, shows error state, and flushes pending save on unmount", async () => {
    const api = editorApi();
    vi.stubGlobal("fetch", api.fetch);
    const { unmount } = renderEditor();

    expect(await screen.findByText("Contoso GmbH")).toBeInTheDocument();

    vi.useFakeTimers();
    fireEvent.change(screen.getByLabelText("Issue date"), {
      target: { value: "2026-07-07" },
    });
    expect(screen.getByRole("status")).toHaveTextContent("Saving");
    act(() => vi.advanceTimersByTime(799));
    expect(api.patchRequests).toHaveLength(0);
    act(() => vi.advanceTimersByTime(1));

    await flushMicrotasks();
    expect(api.patchRequests).toHaveLength(1);
    expect(api.patchRequests[0].issue_date).toBe("2026-07-07");
    expect(screen.getByRole("status")).toHaveTextContent("Saved");

    api.failNextPatch = true;
    fireEvent.change(screen.getByLabelText("Due date"), {
      target: { value: "2026-07-22" },
    });
    act(() => vi.advanceTimersByTime(800));

    await flushMicrotasks();
    expect(screen.getByRole("status")).toHaveTextContent("Error");

    fireEvent.change(screen.getByLabelText("Due date"), {
      target: { value: "2026-07-23" },
    });
    const requestCount = api.patchRequests.length;
    unmount();

    await flushMicrotasks();
    expect(api.patchRequests).toHaveLength(requestCount + 1);
    expect(api.patchRequests.at(-1)?.due_date).toBe("2026-07-23");
  });

  it("recomputes domestic and reverse-charge totals", async () => {
    const user = userEvent.setup();
    const api = editorApi();
    vi.stubGlobal("fetch", api.fetch);
    renderEditor();

    await screen.findByText("Contoso GmbH");
    expect(totalRow("VAT")).toHaveTextContent("€200.00");
    expect(totalRow("Total")).toHaveTextContent("€1,200.00");

    await user.selectOptions(
      screen.getByLabelText("VAT treatment"),
      "reverse-charge-eu-b2b",
    );

    expect(totalRow("VAT")).toHaveTextContent("€0.00");
    expect(totalRow("Total")).toHaveTextContent("€1,000.00");
  });

  it("shows draft indicative FX rate and sent locked rate source", async () => {
    const user = userEvent.setup();
    const api = editorApi();
    vi.stubGlobal("fetch", api.fetch);
    renderEditor();

    await screen.findByText("≈ 0.85");
    expect(screen.getByText("ECB 2026-07-05, locks at send")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Send invoice" }));

    expect((await screen.findAllByText("INV-2026-1")).length).toBeGreaterThan(0);
    expect(await screen.findByText("Source: ECB 2026-07-06")).toBeInTheDocument();
    expect(screen.getByText("🔒 0.850000000000000000")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Revert same-day" }),
    ).toBeInTheDocument();
  });

  it("enforces read-only controls for sent invoices", async () => {
    const api = editorApi({ invoice: sentInvoice() });
    vi.stubGlobal("fetch", api.fetch);
    renderEditor();

    expect((await screen.findAllByText("INV-2026-1")).length).toBeGreaterThan(0);
    expect(screen.getByLabelText("Client")).toBeDisabled();
    expect(screen.getByLabelText("Issue date")).toHaveAttribute("readonly");
    expect(screen.getByLabelText("Due date")).toHaveAttribute("readonly");
    expect(screen.getByLabelText("Description line 1")).toHaveAttribute(
      "readonly",
    );
    expect(
      screen.queryByRole("button", { name: "Add line" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Remove line 1" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Send invoice" }),
    ).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Issue date"), {
      target: { value: "2026-07-08" },
    });
    await new Promise((resolve) => window.setTimeout(resolve, 0));

    expect(api.patchRequests).toHaveLength(0);
  });
});

function renderEditor() {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  return render(
    <MemoryRouter initialEntries={["/invoices/inv_1"]}>
      <QueryClientProvider client={queryClient}>
        <Routes>
          <Route path="/invoices/:id" element={<InvoiceEditorScreen />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function totalRow(label: string) {
  const totalsCard = screen.getByText("Totals").closest("section");
  expect(totalsCard).not.toBeNull();
  const term = within(totalsCard as HTMLElement).getByText(label);
  const row = term.closest("div");
  expect(row).not.toBeNull();
  return row as HTMLElement;
}

async function flushMicrotasks() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function editorApi(options: { invoice?: InvoicingInvoice } = {}) {
  let invoice = options.invoice ?? draftInvoice();
  let updatedSequence = 0;
  const patchRequests: InvoicingInvoicePatch[] = [];
  const api = {
    failNextPatch: false,
    fetch: vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = pathFromRequest(input);
      const method = init?.method ?? "GET";

      if (path === "/api/invoicing/clients") {
        return jsonResponse({ clients: clientsFixture() });
      }
      if (path === "/api/invoicing/invoices/inv_1" && method === "GET") {
        return jsonResponse(invoice);
      }
      if (path === "/api/invoicing/invoices/inv_1" && method === "PATCH") {
        const patch = JSON.parse(String(init?.body)) as InvoicingInvoicePatch;
        patchRequests.push(patch);
        if (api.failNextPatch) {
          api.failNextPatch = false;
          return jsonResponse(
            {
              detail: "database temporarily unavailable",
              status: 503,
              title: "Autosave failed",
              type: "about:blank",
            },
            503,
            "application/problem+json",
          );
        }
        updatedSequence += 1;
        invoice = applyPatch(invoice, patch, updatedSequence);
        return jsonResponse(invoice);
      }
      if (path === "/api/invoicing/invoices/inv_1/send" && method === "POST") {
        const sent = sentInvoice({
          ...invoice,
          number: "INV-2026-1",
          sent_at: "2026-07-06T12:00:00Z",
          status: "sent",
        });
        invoice = sent;
        const result: InvoicingSendInvoiceResult = {
          invoice: sent,
          locked_rate: {
            from: "EUR",
            id: 12,
            rate: "0.850000000000000000",
            rate_date: "2026-07-06",
            source: "ECB",
            to: "GBP",
          },
          number: "INV-2026-1",
        };
        return jsonResponse(result);
      }
      if (
        path === "/api/invoicing/invoices/inv_1/revert" &&
        method === "POST"
      ) {
        invoice = draftInvoice({ ...invoice, number: null, status: "draft" });
        return jsonResponse(invoice);
      }
      return jsonResponse(
        { status: 404, title: "Not Found", type: "about:blank" },
        404,
        "application/problem+json",
      );
    }),
    patchRequests,
  };
  return api;
}

function applyPatch(
  invoice: InvoicingInvoice,
  patch: InvoicingInvoicePatch,
  updatedSequence: number,
): InvoicingInvoice {
  const currency = patch.currency ?? invoice.currency;
  const vatTreatment = patch.vat_treatment ?? invoice.vat_treatment;
  const lines =
    patch.lines?.map((line, index) => {
      const amount = Math.round(Number(line.qty) * line.unit_price.amount);
      return {
        description: line.description,
        id: line.id,
        invoice_id: invoice.id,
        line_total: { amount, currency },
        position: index + 1,
        qty: line.qty,
        unit_price: { amount: line.unit_price.amount, currency },
      };
    }) ?? invoice.lines;
  const subtotal = lines.reduce((sum, line) => sum + line.line_total.amount, 0);
  const vat =
    vatTreatment === "domestic" ? Math.round(subtotal * 0.2) : 0;
  return {
    ...invoice,
    client_id: patch.client_id ?? invoice.client_id,
    currency,
    due_date: patch.due_date
      ? `${patch.due_date}T00:00:00Z`
      : invoice.due_date,
    issue_date: patch.issue_date
      ? `${patch.issue_date}T00:00:00Z`
      : invoice.issue_date,
    lines,
    totals: totals(currency, subtotal, vat),
    updated_at: `2026-07-06T12:00:0${updatedSequence}Z`,
    vat_treatment: vatTreatment,
  };
}

function clientsFixture(): InvoicingClient[] {
  return [
    {
      address: {
        country: "DE",
        line1: "1 Main St",
        line2: "",
        locality: "Berlin",
        postal_code: "10115",
        region: "",
      },
      archived_at: null,
      created_at: "2026-07-01T00:00:00Z",
      day_rate: null,
      default_currency: "EUR",
      email: "billing@contoso.example",
      id: "client_contoso",
      name: "Contoso GmbH",
      retainer_amount: null,
      terms_days: 14,
      vat_number: "DE123",
      vat_treatment: "reverse-charge-eu-b2b",
    },
  ];
}

function draftInvoice(
  overrides: Partial<InvoicingInvoice> = {},
): InvoicingInvoice {
  const currency = overrides.currency ?? "EUR";
  const subtotal = 100000;
  const vat = overrides.vat_treatment === "reverse-charge-eu-b2b" ? 0 : 20000;
  return {
    client_id: "client_contoso",
    created_at: "2026-07-06T10:00:00Z",
    currency,
    due_date: "2026-07-20T00:00:00Z",
    id: "inv_1",
    issue_date: "2026-07-06T00:00:00Z",
    lines: [
      {
        description: "July consulting",
        id: "line_1",
        invoice_id: "inv_1",
        line_total: { amount: subtotal, currency },
        position: 1,
        qty: "1",
        unit_price: { amount: subtotal, currency },
      },
    ],
    lock_id: null,
    number: null,
    pdf_asset: null,
    reminders: [],
    sent_at: null,
    settled_amount: null,
    settled_date: null,
    settlement_txn_ref: null,
    status: "draft",
    totals: totals(currency, subtotal, vat),
    updated_at: "2026-07-06T10:00:00Z",
    vat_treatment: "domestic",
    ...overrides,
  };
}

function sentInvoice(
  overrides: Partial<InvoicingInvoice> = {},
): InvoicingInvoice {
  const base = draftInvoice({
    lock_id: "12",
    number: "INV-2026-1",
    sent_at: "2026-07-06T12:00:00Z",
    status: "sent",
    ...overrides,
  });
  return {
    ...base,
    totals: {
      ...base.totals,
      approx_gbp: null,
    },
  };
}

function totals(currency: "EUR" | "GBP", subtotal: number, vat: number) {
  return {
    approx_gbp: {
      amount: { amount: Math.round((subtotal + vat) * 0.85), currency: "GBP" },
      as_of: "2026-07-06T09:00:00Z",
      locked: false,
      rate: {
        from: currency,
        rate_date: "2026-07-05T00:00:00Z",
        source: "ECB",
        to: "GBP",
        value: "0.85",
      },
    },
    subtotal: { amount: subtotal, currency },
    total: { amount: subtotal + vat, currency },
    vat: { amount: vat, currency },
  } satisfies InvoicingInvoice["totals"];
}

function pathFromRequest(input: RequestInfo | URL) {
  if (input instanceof Request) {
    return new URL(input.url, "http://localhost").pathname;
  }
  return new URL(String(input), "http://localhost").pathname;
}

function jsonResponse(
  body: unknown,
  status = 200,
  contentType = "application/json",
) {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": contentType },
    status,
  });
}
