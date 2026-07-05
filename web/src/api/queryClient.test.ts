import { describe, expect, it } from "vitest";

import { ApiError } from "@/api/client";
import { retryApiFailure } from "@/api/queryClient";

describe("retryApiFailure", () => {
  it("does not retry 4xx ApiError failures", () => {
    const error = new ApiError(
      new Response(null, { status: 404, statusText: "Not Found" }),
      {
        status: 404,
        title: "Not Found",
        type: "about:blank",
      },
    );

    expect(retryApiFailure(0, error)).toBe(false);
  });

  it("allows limited retries for non-4xx failures", () => {
    expect(retryApiFailure(0, new Error("network"))).toBe(true);
    expect(retryApiFailure(2, new Error("network"))).toBe(false);
  });
});
