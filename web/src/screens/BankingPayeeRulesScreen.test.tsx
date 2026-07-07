import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { BankingPayeeRule, BankingPayeeRuleRequest } from "@/api/banking";
import { BankingPayeeRulesScreen } from "@/screens/BankingPayeeRulesScreen";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("BankingPayeeRulesScreen", () => {
  it("lists, creates, edits, and deletes payee rules", async () => {
    const user = userEvent.setup();
    const api = payeeRulesApi([
      payeeRule({
        account_code: "5000-fees",
        created_from: "recode",
        matcher: "acme saas",
        times_applied: 3,
      }),
    ]);
    vi.stubGlobal("fetch", api.fetch);
    vi.spyOn(window, "confirm").mockReturnValue(true);

    renderRules();

    expect(await screen.findByText("acme saas")).toBeInTheDocument();
    const initialAcmeRow = rowForText("acme saas");
    expect(within(initialAcmeRow).getByText("Fees")).toBeInTheDocument();
    expect(within(initialAcmeRow).getByText("Recode")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Banking queue" })).toHaveAttribute(
      "href",
      "/banking",
    );

    await user.type(screen.getByLabelText("Matcher"), "New Vendor");
    await user.selectOptions(screen.getByLabelText("Mode"), "contains");
    await user.selectOptions(screen.getByLabelText("Category"), "5030-office");
    await user.click(screen.getByRole("button", { name: "Create rule" }));

    expect(await screen.findByText("new vendor")).toBeInTheDocument();
    expect(api.lastCreateBody()).toEqual({
      account_code: "5030-office",
      match_mode: "contains",
      matcher: "New Vendor",
    });

    const acmeRow = rowForText("acme saas");
    await user.click(within(acmeRow).getByRole("button", { name: "Edit" }));
    const categorySelect = await screen.findByLabelText(
      "Category for acme saas",
    );
    await user.selectOptions(categorySelect, "5010-software");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(rowForText("acme saas")).toHaveTextContent("Software"),
    );
    expect(api.lastUpdateBody()).toEqual({
      account_code: "5010-software",
      match_mode: "exact",
      matcher: "acme saas",
    });

    await user.click(
      within(rowForText("acme saas")).getByRole("button", { name: "Delete" }),
    );

    await waitFor(() =>
      expect(screen.queryByText("acme saas")).not.toBeInTheDocument(),
    );
    expect(window.confirm).toHaveBeenCalledWith(
      "Delete payee rule for acme saas?",
    );
  });
});

function renderRules() {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <BankingPayeeRulesScreen />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function payeeRulesApi(initialRules: BankingPayeeRule[]) {
  let rules = [...initialRules];
  const createBodies: BankingPayeeRuleRequest[] = [];
  const updateBodies: BankingPayeeRuleRequest[] = [];
  return {
    fetch: vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = urlFromRequest(input);
      const path = url.pathname;
      const method = init?.method ?? "GET";

      if (path === "/api/banking/payee-rules" && method === "GET") {
        return jsonResponse({ rules });
      }

      if (path === "/api/banking/payee-rules" && method === "POST") {
        const body = readJSONBody(init) as BankingPayeeRuleRequest;
        createBodies.push(body);
        const rule = payeeRule({
          account_code: body.account_code,
          created_from: "manual",
          id: nextRuleID(rules),
          match_mode: body.match_mode,
          matcher: body.matcher.toLowerCase(),
          times_applied: 0,
        });
        rules = [...rules, rule];
        return jsonResponse(rule, 201);
      }

      const match = path.match(/^\/api\/banking\/payee-rules\/(\d+)$/);
      if (match && method === "PUT") {
        const id = Number(match[1]);
        const body = readJSONBody(init) as BankingPayeeRuleRequest;
        updateBodies.push(body);
        const current = rules.find((rule) => rule.id === id);
        if (!current) {
          return problemResponse(404);
        }
        const updated = {
          ...current,
          account_code: body.account_code,
          match_mode: body.match_mode,
          matcher: body.matcher,
        };
        rules = rules.map((rule) => (rule.id === id ? updated : rule));
        return jsonResponse(updated);
      }

      if (match && method === "DELETE") {
        const id = Number(match[1]);
        rules = rules.filter((rule) => rule.id !== id);
        return new Response(null, { status: 204 });
      }

      return problemResponse(404);
    }),
    lastCreateBody: () => createBodies.at(-1),
    lastUpdateBody: () => updateBodies.at(-1),
  };
}

function payeeRule(
  overrides: Partial<BankingPayeeRule> = {},
): BankingPayeeRule {
  return {
    account_code: "5010-software",
    created_at: "2026-07-07T09:00:00Z",
    created_from: "manual",
    id: 1,
    last_applied_at: null,
    match_mode: "exact",
    matcher: "saas vendor",
    times_applied: 0,
    ...overrides,
  };
}

function nextRuleID(rules: BankingPayeeRule[]) {
  return rules.reduce((max, rule) => Math.max(max, rule.id), 0) + 1;
}

function rowForText(text: string) {
  const row = screen.getByText(text).closest("tr");
  expect(row).not.toBeNull();
  return row as HTMLElement;
}

function readJSONBody(init: RequestInit | undefined) {
  return JSON.parse(String(init?.body ?? "{}")) as unknown;
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

function problemResponse(status: number) {
  return jsonResponse(
    { status, title: "Request failed", type: "about:blank" },
    status,
    "application/problem+json",
  );
}
