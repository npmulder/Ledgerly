import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { EmptyState } from "@/components/EmptyState";

describe("EmptyState", () => {
  it("renders the all caught up pattern", () => {
    render(
      <EmptyState>
        All caught up — every transaction to 02 Jul is coded.
      </EmptyState>,
    );

    expect(
      screen.getByRole("heading", { name: "All caught up" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/every transaction to 02 Jul is coded/i),
    ).toBeInTheDocument();
  });
});
