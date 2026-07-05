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
      expect.objectContaining({ method: "GET" }),
    );
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
