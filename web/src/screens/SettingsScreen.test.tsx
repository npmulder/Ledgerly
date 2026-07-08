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

import { App } from "@/app/App";
import type {
  IdentityPAT,
  IdentityProfile,
  IdentityProfilePatch,
} from "@/api/identity";
import type { InvoicingClient } from "@/api/invoicing";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("Company settings", () => {
  it("saves the trading name and updates the header identity", async () => {
    const user = userEvent.setup();
    let profile = identityProfile();
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = pathFromRequest(input);
        if (path === "/api/identity/me") {
          return jsonResponse({
            created_at: "2026-07-05T12:00:00Z",
            email: "owner@example.com",
            id: 1,
            name: "N. Meyer",
          });
        }
        if (path === "/api/identity/profile" && init?.method === "PATCH") {
          const patch = JSON.parse(
            String(init.body),
          ) as Partial<IdentityProfile>;
          profile = {
            ...profile,
            company_number: patch.company_number ?? profile.company_number,
            incorporation_date:
              patch.incorporation_date ?? profile.incorporation_date,
            is_vat_registered:
              patch.is_vat_registered ?? profile.is_vat_registered,
            legal_name: patch.legal_name ?? profile.legal_name,
            registered_office:
              patch.registered_office ?? profile.registered_office,
            trading_name: patch.trading_name ?? profile.trading_name,
            vat_number:
              patch.vat_number === undefined
                ? profile.vat_number
                : patch.vat_number,
            year_end: patch.year_end ?? profile.year_end,
          };
          return jsonResponse(profile);
        }
        if (path === "/api/identity/profile") {
          return jsonResponse(profile);
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/company");

    const tradingName = await screen.findByLabelText("Trading name");
    await user.clear(tradingName);
    await user.type(tradingName, "Keel Holdings");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(
        within(screen.getByRole("banner")).getByText("Keel Holdings"),
      ).toBeInTheDocument();
    });
    expect(fetchImpl).toHaveBeenCalledWith(
      "/api/identity/profile",
      expect.objectContaining({ method: "PATCH" }),
    );
  });

  it("saves VAT registration status and warns when the VAT number is stranded", async () => {
    const user = userEvent.setup();
    let profile = identityProfile();
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = pathFromRequest(input);
        if (path === "/api/identity/me") {
          return jsonResponse({
            created_at: "2026-07-05T12:00:00Z",
            email: "owner@example.com",
            id: 1,
            name: "N. Meyer",
          });
        }
        if (path === "/api/identity/profile" && init?.method === "PATCH") {
          const patch = JSON.parse(String(init.body)) as IdentityProfilePatch;
          profile = {
            ...profile,
            is_vat_registered:
              patch.is_vat_registered ?? profile.is_vat_registered,
            vat_number:
              patch.vat_number === undefined
                ? profile.vat_number
                : patch.vat_number,
          };
          return jsonResponse(profile);
        }
        if (path === "/api/identity/profile") {
          return jsonResponse(profile);
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/company");

    const vatRegistered = await screen.findByLabelText("VAT registered");
    const vatNumber = screen.getByLabelText("VAT number");
    await user.type(vatNumber, "IM1234567");

    expect(
      screen.getByText("VAT number is present while VAT registered is off."),
    ).toBeInTheDocument();

    await user.click(vatRegistered);
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(fetchImpl).toHaveBeenCalledWith(
        "/api/identity/profile",
        expect.objectContaining({
          body: expect.stringContaining('"is_vat_registered":true'),
          method: "PATCH",
        }),
      );
    });
    expect(screen.queryByText(/VAT number is present/)).not.toBeInTheDocument();
  });

  it("edits company directors", async () => {
    const user = userEvent.setup();
    let profile = identityProfile();
    let savedPatch: IdentityProfilePatch | null = null;
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = pathFromRequest(input);
        if (path === "/api/identity/me") {
          return jsonResponse({
            created_at: "2026-07-05T12:00:00Z",
            email: "owner@example.com",
            id: 1,
            name: "N. Meyer",
          });
        }
        if (path === "/api/identity/profile" && init?.method === "PATCH") {
          savedPatch = JSON.parse(String(init.body)) as IdentityProfilePatch;
          profile = {
            ...profile,
            directors: savedPatch.directors ?? profile.directors,
          };
          return jsonResponse(profile);
        }
        if (path === "/api/identity/profile") {
          return jsonResponse(profile);
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/company");

    const firstDirector = await screen.findByLabelText("Director 1 name");
    await user.clear(firstDirector);
    await user.type(firstDirector, "Neil Meyer");
    await user.click(screen.getByRole("button", { name: "+ Add director" }));
    await user.type(screen.getByLabelText("Director 2 name"), "Jane Roberts");
    await user.type(
      screen.getByLabelText("Director 2 appointed date"),
      "2024-02-01",
    );
    await user.click(
      within(
        screen
          .getByLabelText("Director 2 appointed date")
          .closest(".director-row") as HTMLElement,
      ).getByLabelText("Chair"),
    );
    await user.click(screen.getByLabelText("Remove director Neil Meyer"));
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(savedPatch?.directors).toEqual([
        {
          appointed_date: "2024-02-01",
          is_chair: true,
          name: "Jane Roberts",
        },
      ]);
    });
  });

  it("offers a pack-backed act suggestion without silently saving it", async () => {
    const user = userEvent.setup();
    let profile = identityProfile();
    let savedPatch: IdentityProfilePatch | null = null;
    let advisorRequestCount = 0;
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = pathFromRequest(input);
        if (path === "/api/identity/me") {
          return jsonResponse({
            created_at: "2026-07-05T12:00:00Z",
            email: "owner@example.com",
            id: 1,
            name: "N. Meyer",
          });
        }
        if (path === "/api/jurisdiction/pack") {
          return jsonResponse(jurisdictionPack());
        }
        if (path === "/api/advisor/insights") {
          advisorRequestCount += 1;
          return jsonResponse({ insights: [] });
        }
        if (path === "/api/identity/profile" && init?.method === "PATCH") {
          savedPatch = JSON.parse(String(init.body)) as IdentityProfilePatch;
          profile = {
            ...profile,
            act_type:
              savedPatch.act_type === undefined
                ? profile.act_type
                : savedPatch.act_type,
          };
          return jsonResponse(profile);
        }
        if (path === "/api/identity/profile") {
          return jsonResponse(profile);
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/company");

    const actSelect = await screen.findByLabelText("Company Act");
    expect(actSelect).toHaveValue("");
    expect(
      await screen.findByText(
        "Suggested from company number: Companies Act 1931.",
      ),
    ).toBeInTheDocument();

    await user.selectOptions(actSelect, "companies-act-1931");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(savedPatch?.act_type).toBe("companies-act-1931");
    });
    await waitFor(() => {
      expect(advisorRequestCount).toBeGreaterThanOrEqual(2);
    });
  });

  it("renders placeholder settings sections as coming soon stubs", async () => {
    vi.stubGlobal("fetch", authenticatedFetch());

    renderAt("/settings/invoicing-defaults");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Invoicing defaults",
      }),
    ).toBeInTheDocument();
    expect(screen.getByText("Coming soon")).toBeInTheDocument();
  });

  it("creates and revokes personal access tokens from Users settings", async () => {
    const user = userEvent.setup();
    let tokens: IdentityPAT[] = [
      {
        created_at: "2026-07-05T12:00:00Z",
        expires_at: null,
        id: 1,
        last_used_at: "2026-07-06T09:00:00Z",
        name: "CLI read",
        scope: "read-only",
      },
    ];
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = pathFromRequest(input);
        if (path === "/api/identity/me") {
          return jsonResponse({
            created_at: "2026-07-05T12:00:00Z",
            email: "owner@example.com",
            id: 1,
            name: "N. Meyer",
          });
        }
        if (path === "/api/identity/pats" && init?.method === "POST") {
          const request = JSON.parse(String(init.body)) as {
            name: string;
            scope: "read-only" | "full";
          };
          const created = {
            created_at: "2026-07-06T10:00:00Z",
            expires_at: null,
            id: 2,
            last_used_at: null,
            name: request.name,
            scope: request.scope,
          };
          tokens = [created, ...tokens];
          return jsonResponse({
            personal_access_token: created,
            token: "lgy_created",
          });
        }
        if (path === "/api/identity/pats/1" && init?.method === "DELETE") {
          tokens = tokens.filter((token) => token.id !== 1);
          return new Response(null, { status: 204 });
        }
        if (path === "/api/identity/pats") {
          return jsonResponse({ tokens });
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/users");

    expect(await screen.findByText("CLI read")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Token name"), "Codex CLI");
    await user.selectOptions(screen.getByLabelText("Scope"), "full");
    await user.click(screen.getByRole("button", { name: "Create token" }));

    expect(await screen.findByDisplayValue("lgy_created")).toBeInTheDocument();
    expect(await screen.findByText("Codex CLI")).toBeInTheDocument();

    const oldTokenRow = screen.getByText("CLI read").closest("tr");
    expect(oldTokenRow).not.toBeNull();
    await user.click(
      within(oldTokenRow as HTMLElement).getByRole("button", {
        name: "Revoke",
      }),
    );

    await waitFor(() => {
      expect(screen.queryByText("CLI read")).not.toBeInTheDocument();
    });
  });

  it("renders clients and archives them from the active list", async () => {
    const user = userEvent.setup();
    let clients = [contosoClient(), fabrikamClient()];
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = pathFromRequest(input);
        if (path === "/api/identity/me") {
          return jsonResponse({
            created_at: "2026-07-05T12:00:00Z",
            email: "owner@example.com",
            id: 1,
            name: "N. Meyer",
          });
        }
        if (path === "/api/identity/profile") {
          return jsonResponse(identityProfile());
        }
        if (
          path === "/api/invoicing/clients/client_fabrikam/archive" &&
          init?.method === "POST"
        ) {
          clients = clients.filter((client) => client.id !== "client_fabrikam");
          return new Response(null, { status: 204 });
        }
        if (path === "/api/invoicing/clients") {
          return jsonResponse({ clients });
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/clients");

    expect(await screen.findByText("Contoso GmbH")).toBeInTheDocument();
    expect(screen.getByText("Fabrikam Ltd")).toBeInTheDocument();
    const contosoRow = screen.getByText("Contoso GmbH").closest("tr");
    const fabrikamRow = screen.getByText("Fabrikam Ltd").closest("tr");
    expect(contosoRow).not.toBeNull();
    expect(fabrikamRow).not.toBeNull();
    expect(
      within(contosoRow as HTMLElement).getByText("EUR"),
    ).toBeInTheDocument();
    expect(
      within(fabrikamRow as HTMLElement).getByText("GBP"),
    ).toBeInTheDocument();
    expect(
      within(contosoRow as HTMLElement).getByText(/Retainer/),
    ).toHaveTextContent("€4,500.00");
    expect(
      within(fabrikamRow as HTMLElement).getByText(/Day rate/),
    ).toHaveTextContent("£600.00");
    expect(
      within(contosoRow as HTMLElement).getByText("Net 14 · reverse charge"),
    ).toBeInTheDocument();
    expect(
      within(fabrikamRow as HTMLElement).getByText("Net 30 · domestic"),
    ).toBeInTheDocument();
    await user.click(
      within(fabrikamRow as HTMLElement).getByRole("button", {
        name: "Archive",
      }),
    );

    await waitFor(() => {
      expect(screen.queryByText("Fabrikam Ltd")).not.toBeInTheDocument();
    });
  });

  it("renders the jurisdiction rules pack from the API", async () => {
    const fetchImpl = authenticatedFetch();
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/jurisdiction");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Jurisdiction",
      }),
    ).toBeInTheDocument();
    expect(
      await screen.findByText("Isle of Man rules pack"),
    ).toBeInTheDocument();
    expect(screen.getByText("v1.0")).toBeInTheDocument();
    expect(screen.getByText("0% CIT (2025-26)")).toBeInTheDocument();
    expect(
      screen.getByText(/VAT 20% via Isle of Man Customs & Excise/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /rules packs are installable modules.+adding a jurisdiction adds a pack/,
      ),
    ).toBeInTheDocument();
    expect(
      within(screen.getByLabelText("Jurisdiction rule summaries")).getAllByRole(
        "listitem",
      ),
    ).toHaveLength(6);
    expect(fetchImpl).toHaveBeenCalledWith(
      "/api/jurisdiction/pack",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("uploads a replacement logo and updates the header logo", async () => {
    const user = userEvent.setup();
    const logoUrl = "/api/identity/assets/17830098-8109-4a00-8b00-000000000002";
    let profile = identityProfile();
    Object.defineProperty(URL, "createObjectURL", {
      configurable: true,
      value: vi.fn(() => "blob:logo-preview"),
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      configurable: true,
      value: vi.fn(),
    });
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = pathFromRequest(input);
        if (path === "/api/identity/me") {
          return jsonResponse({
            created_at: "2026-07-05T12:00:00Z",
            email: "owner@example.com",
            id: 1,
            name: "N. Meyer",
          });
        }
        if (path === "/api/identity/logo" && init?.method === "PUT") {
          expect(init.body).toBeInstanceOf(FormData);
          profile = {
            ...profile,
            logo_asset_id: "17830098-8109-4a00-8b00-000000000002",
            logo_asset_url: logoUrl,
          };
          return jsonResponse({
            asset_id: "17830098-8109-4a00-8b00-000000000002",
            asset_url: logoUrl,
          });
        }
        if (path === "/api/identity/profile") {
          return jsonResponse(profile);
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/company");

    await screen.findByLabelText("Trading name");
    expect(screen.getByLabelText("Company logo file")).toHaveAttribute(
      "accept",
      "image/png,image/jpeg",
    );
    await user.upload(
      screen.getByLabelText("Company logo file"),
      new File(["logo"], "logo.jpg", { type: "image/jpeg" }),
    );

    await waitFor(() => {
      const headerLogo = screen
        .getByRole("banner")
        .querySelector(".app-shell__logo-img");
      expect(headerLogo).not.toBeNull();
      expect(headerLogo as HTMLElement).toHaveAttribute("src", logoUrl);
    });
  });

  it("renders a creation form when the company profile is missing", async () => {
    const user = userEvent.setup();
    let profile: IdentityProfile | null = null;
    const fetchImpl = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const path = pathFromRequest(input);
        if (path === "/api/identity/me") {
          return jsonResponse({
            created_at: "2026-07-05T12:00:00Z",
            email: "owner@example.com",
            id: 1,
            name: "N. Meyer",
          });
        }
        if (path === "/api/identity/profile" && init?.method === "PATCH") {
          const patch = JSON.parse(String(init.body)) as IdentityProfilePatch;
          profile = {
            ...identityProfile(),
            company_number: patch.company_number ?? "",
            incorporation_date: patch.incorporation_date ?? "",
            is_vat_registered: patch.is_vat_registered ?? false,
            legal_name: patch.legal_name ?? "",
            registered_office: patch.registered_office ?? {
              country: "",
              line1: "",
              line2: "",
              locality: "",
              postal_code: "",
              region: "",
            },
            trading_name: patch.trading_name ?? "",
            vat_number: patch.vat_number ?? null,
            year_end: patch.year_end ?? { day: 31, month: 3 },
          };
          return jsonResponse(profile);
        }
        if (path === "/api/identity/profile" && profile) {
          return jsonResponse(profile);
        }
        if (path === "/api/identity/profile") {
          return jsonResponse(
            { status: 404, title: "Not Found", type: "about:blank" },
            404,
            "application/problem+json",
          );
        }
        return jsonResponse(
          { status: 404, title: "Not Found", type: "about:blank" },
          404,
          "application/problem+json",
        );
      },
    );
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/company");

    expect(
      await screen.findByRole("button", { name: "Create company profile" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save changes" })).toBeNull();
    expect(screen.queryByLabelText("Company logo file")).toBeNull();
    expect(screen.queryByLabelText("VAT registered")).toBeNull();
    expect(screen.queryByLabelText("VAT number")).toBeNull();

    await user.type(await screen.findByLabelText("Trading name"), "Keel Newco");
    await user.type(screen.getByLabelText("Legal name"), "Keel Newco Limited");
    await user.type(screen.getByLabelText("Company number"), "020263C");
    await user.type(
      screen.getByLabelText("Registered office line 1"),
      "1 Quay",
    );
    await user.type(
      screen.getByLabelText("Registered office locality"),
      "Douglas",
    );
    await user.type(screen.getByLabelText("Registered office country"), "IM");
    await user.type(screen.getByLabelText("Incorporation date"), "2026-07-05");
    await user.click(
      screen.getByRole("button", { name: "Create company profile" }),
    );

    await waitFor(() => {
      expect(
        within(screen.getByRole("banner")).getByText("Keel Newco"),
      ).toBeInTheDocument();
    });
    expect(
      screen.getByRole("button", { name: "Save changes" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Company logo file")).toBeInTheDocument();
    expect(screen.getByLabelText("VAT registered")).toBeInTheDocument();
    expect(fetchImpl).toHaveBeenCalledWith(
      "/api/identity/profile",
      expect.objectContaining({ method: "PATCH" }),
    );
  });

  it("keeps non-404 company profile load failures in the error state", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const path = pathFromRequest(input);
      if (path === "/api/identity/me") {
        return jsonResponse({
          created_at: "2026-07-05T12:00:00Z",
          email: "owner@example.com",
          id: 1,
          name: "N. Meyer",
        });
      }
      if (path === "/api/identity/profile") {
        return jsonResponse(
          {
            detail: "profile database is unavailable",
            status: 503,
            title: "Service unavailable",
            type: "about:blank",
          },
          503,
          "application/problem+json",
        );
      }
      return jsonResponse(
        { status: 404, title: "Not Found", type: "about:blank" },
        404,
        "application/problem+json",
      );
    });
    vi.stubGlobal("fetch", fetchImpl);

    renderAt("/settings/company");

    expect(await screen.findByText("Service unavailable")).toBeInTheDocument();
    expect(
      screen.getByText("profile database is unavailable"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Create company profile" }),
    ).toBeNull();
  });
});

function renderAt(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });

  render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function authenticatedFetch() {
  return vi.fn(async (input: RequestInfo | URL) => {
    const path = pathFromRequest(input);
    if (path === "/api/identity/me") {
      return jsonResponse({
        created_at: "2026-07-05T12:00:00Z",
        email: "owner@example.com",
        id: 1,
        name: "N. Meyer",
      });
    }
    if (path === "/api/identity/profile") {
      return jsonResponse(identityProfile());
    }
    if (path === "/api/invoicing/clients") {
      return jsonResponse({ clients: [contosoClient(), fabrikamClient()] });
    }
    if (path === "/api/jurisdiction/pack") {
      return jsonResponse(jurisdictionPack());
    }
    return jsonResponse(
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
      "application/problem+json",
    );
  });
}

function jurisdictionPack() {
  return {
    company_acts: [
      {
        act_type: "companies-act-1931",
        company_number_suffixes: ["C"],
        corporate_directors: false,
        label: "Companies Act 1931",
        minimum_directors: 2,
      },
      {
        act_type: "companies-act-2006",
        company_number_suffixes: ["V"],
        corporate_directors: null,
        label: "Companies Act 2006",
        minimum_directors: 1,
      },
    ],
    meta: {
      id: "isle-of-man",
      name: "Isle of Man",
      version: "1.0",
    },
    rule_summaries: [
      {
        id: "corporate_income_tax",
        label: "Corporate income tax",
        summary: "0% CIT (2025-26)",
      },
      {
        id: "personal_tax_dividends",
        label: "Personal tax and dividends",
        summary:
          "no dividend WHT; personal allowance GBP 14,750; bands 10% to GBP 6,500, then 21% (2025-26/2025-26)",
      },
      {
        id: "vat",
        label: "VAT",
        summary:
          "VAT 20% via Isle of Man Customs & Excise; reverse charge via Article 196, Directive 2006/112/EC (2025-26)",
      },
      {
        id: "annual_return",
        label: "Annual return",
        summary:
          "due incorporation anniversary + 1 month with IoM Companies Registry",
      },
      {
        id: "company_tax_return",
        label: "Company tax return",
        summary:
          "due accounting year end + 12 months + 1 day; required at zero rate",
      },
      {
        id: "director_loan",
        label: "Director loan account",
        summary:
          "no s455 charge; overdrawn warning: benefit in kind interest free; remedy: clear with dividend",
      },
    ],
  };
}

function identityProfile(): IdentityProfile {
  return {
    act_type: null,
    bank_details: { bank_name: "", bic: "", iban: "" },
    company_number: "137792C",
    incorporation_date: "2020-07-14",
    is_vat_registered: false,
    legal_name: "NPM Limited",
    logo_asset_id: null,
    logo_asset_url: null,
    registered_office: {
      country: "IM",
      line1: "18 Athol St",
      line2: "",
      locality: "Douglas",
      postal_code: "",
      region: "",
    },
    shareholders: [
      {
        class: "ordinary £1",
        name: "N. Meyer",
        shares: 100,
      },
    ],
    directors: [
      {
        appointed_date: "2020-07-14",
        is_chair: true,
        name: "N. Meyer",
      },
    ],
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
  };
}

function contosoClient(): InvoicingClient {
  return {
    address: {
      country: "DE",
      line1: "Theresienhoehe 12",
      line2: "",
      locality: "Munich",
      postal_code: "80339",
      region: "Bavaria",
    },
    archived_at: null,
    created_at: "2026-07-05T12:00:00Z",
    day_rate: null,
    default_currency: "EUR",
    email: "billing@contoso.example",
    id: "client_contoso",
    name: "Contoso GmbH",
    retainer_amount: {
      amount_minor: 450000,
      currency: "EUR",
    },
    terms_days: 14,
    vat_number: "DE 129 273 398",
    vat_treatment: "reverse-charge-eu-b2b",
  };
}

function fabrikamClient(): InvoicingClient {
  return {
    address: {
      country: "GB",
      line1: "1 Park Row",
      line2: "",
      locality: "Leeds",
      postal_code: "LS1 5AB",
      region: "West Yorkshire",
    },
    archived_at: null,
    created_at: "2026-07-05T12:00:00Z",
    day_rate: {
      amount_minor: 60000,
      currency: "GBP",
    },
    default_currency: "GBP",
    email: "accounts@fabrikam.example",
    id: "client_fabrikam",
    name: "Fabrikam Ltd",
    retainer_amount: null,
    terms_days: 30,
    vat_number: null,
    vat_treatment: "domestic",
  };
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
    headers: {
      "Content-Type": contentType,
    },
    status,
  });
}
