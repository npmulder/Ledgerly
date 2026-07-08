import type { ReactElement } from "react";
import { cleanup, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AuditHistoryPanel } from "@/components/AuditHistoryPanel";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AuditHistoryPanel", () => {
  it("loads and renders before/after field changes", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = urlFromRequest(input);
      expect(url.pathname).toBe(
        "/api/audit/history/invoicing/client/client_contoso",
      );
      expect(url.searchParams.get("limit")).toBe("50");
      return jsonResponse({
        entries: [
          {
            actor: "owner@example.com",
            diff: {
              vat_number: {
                after: "DE123",
                before: null,
              },
            },
            entity: "client",
            entity_id: "client_contoso",
            id: 7,
            module: "invoicing",
            occurred_at: "2026-07-07T12:30:00Z",
          },
        ],
      });
    });
    vi.stubGlobal("fetch", fetchImpl);

    renderWithClient(
      <AuditHistoryPanel
        entity="client"
        entityId="client_contoso"
        module="invoicing"
      />,
    );

    const panel = screen.getByLabelText("History");
    expect(
      await within(panel).findByText("owner@example.com"),
    ).toBeInTheDocument();
    expect(within(panel).getByText("vat number")).toBeInTheDocument();
    expect(within(panel).getByText("empty")).toBeInTheDocument();
    expect(within(panel).getByText("DE123")).toBeInTheDocument();
  });
});

function renderWithClient(element: ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>{element}</QueryClientProvider>,
  );
}

function urlFromRequest(input: RequestInfo | URL) {
  const url =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.toString()
        : input.url;
  return new URL(url, "http://ledgerly.test");
}

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    headers: { "Content-Type": "application/json" },
    status: 200,
  });
}
