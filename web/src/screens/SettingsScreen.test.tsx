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
import type { IdentityProfile } from "@/api/identity";

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

  it("renders non-company settings sections as coming soon stubs", async () => {
    vi.stubGlobal("fetch", authenticatedFetch());

    renderAt("/settings/users");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Users" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Coming soon")).toBeInTheDocument();
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
    await user.upload(
      screen.getByLabelText("Company logo file"),
      new File(["<svg></svg>"], "logo.svg", { type: "image/svg+xml" }),
    );

    await waitFor(() => {
      const headerLogo = screen
        .getByRole("banner")
        .querySelector(".app-shell__logo-img");
      expect(headerLogo).not.toBeNull();
      expect(headerLogo as HTMLElement).toHaveAttribute("src", logoUrl);
    });
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
    return jsonResponse(
      { status: 404, title: "Not Found", type: "about:blank" },
      404,
      "application/problem+json",
    );
  });
}

function identityProfile(): IdentityProfile {
  return {
    bank_details: { bank_name: "", bic: "", iban: "" },
    company_number: "137792C",
    incorporation_date: "2020-07-14",
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
    trading_name: "NPM Limited",
    vat_number: null,
    year_end: { day: 31, month: 3 },
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
