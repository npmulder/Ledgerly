import { describe, expect, it, vi } from "vitest";

import { ApiClient, ApiError } from "@/api/client";

describe("ApiClient", () => {
  it("returns generated health response data for successful GETs", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      jsonResponse(
        {
          checks: {
            db: { status: "ok" },
          },
          status: "ok",
          version: "test-version",
        },
        200,
      ),
    );
    const client = new ApiClient({ fetchImpl });

    await expect(client.get("/healthz")).resolves.toMatchObject({
      status: "ok",
      version: "test-version",
    });
    expect(fetchImpl).toHaveBeenCalledWith(
      "/healthz",
      expect.objectContaining({ credentials: "same-origin", method: "GET" }),
    );
  });

  it("sends JSON mutation bodies with generated route typing", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      jsonResponse(
        {
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
          shareholders: [],
          trading_name: "NPM Trading",
          vat_number: null,
          year_end: { day: 31, month: 3 },
        },
        200,
      ),
    );
    const client = new ApiClient({ fetchImpl });

    await client.patch("/api/identity/profile", {
      trading_name: "NPM Trading",
    });

    const [, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
    expect(init).toMatchObject({
      body: JSON.stringify({ trading_name: "NPM Trading" }),
      credentials: "same-origin",
      method: "PATCH",
    });
    expect(new Headers(init.headers).get("Content-Type")).toBe(
      "application/json",
    );
  });

  it("sends multipart bodies without forcing a JSON content type", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      jsonResponse(
        {
          asset_id: "17830098-8109-4a00-8b00-000000000001",
          asset_url:
            "/api/identity/assets/17830098-8109-4a00-8b00-000000000001",
        },
        200,
      ),
    );
    const client = new ApiClient({ fetchImpl });
    const body = new FormData();
    body.append("logo", new Blob(["logo"], { type: "image/png" }));

    await client.put("/api/identity/logo", body);

    const [, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
    expect(init).toMatchObject({
      body,
      credentials: "same-origin",
      method: "PUT",
    });
    expect(new Headers(init.headers).get("Content-Type")).toBeNull();
  });

  it("throws ApiError with parsed RFC 7807 problem details", async () => {
    const client = new ApiClient({
      fetchImpl: vi.fn().mockResolvedValue(
        jsonResponse(
          {
            detail: "database ping failed",
            status: 503,
            title: "Service Unavailable",
            type: "about:blank",
            version: "test-version",
          },
          503,
          "Service Unavailable",
          "application/problem+json",
        ),
      ),
    });

    await expect(client.get("/healthz")).rejects.toMatchObject({
      problem: {
        detail: "database ping failed",
        title: "Service Unavailable",
        version: "test-version",
      },
      status: 503,
    });
  });

  it("runs the unauthorized handler for 401 problem responses", async () => {
    const onUnauthorized = vi.fn();
    const client = new ApiClient({
      fetchImpl: vi.fn().mockResolvedValue(
        jsonResponse(
          {
            status: 401,
            title: "Unauthorized",
            type: "about:blank",
          },
          401,
          "Unauthorized",
          "application/problem+json",
        ),
      ),
      onUnauthorized,
    });

    await expect(client.get("/healthz")).rejects.toBeInstanceOf(ApiError);
    expect(onUnauthorized).toHaveBeenCalledOnce();
  });

  it("can skip the unauthorized handler for expected 401 responses", async () => {
    const onUnauthorized = vi.fn();
    const client = new ApiClient({
      fetchImpl: vi.fn().mockResolvedValue(
        jsonResponse(
          {
            status: 401,
            title: "Unauthorized",
            type: "about:blank",
          },
          401,
          "Unauthorized",
          "application/problem+json",
        ),
      ),
      onUnauthorized,
    });

    await expect(
      client.post(
        "/api/identity/login",
        {
          email: "owner@example.com",
          password: "wrong",
        },
        { handleUnauthorized: false },
      ),
    ).rejects.toBeInstanceOf(ApiError);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});

function jsonResponse(
  body: unknown,
  status: number,
  statusText = "OK",
  contentType = "application/json",
) {
  return new Response(JSON.stringify(body), {
    headers: {
      "Content-Type": contentType,
    },
    status,
    statusText,
  });
}
