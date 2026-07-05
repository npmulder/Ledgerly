import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { APP_VERSION } from "@/app/version";
import { ProductMark } from "@/components/ProductMark";

describe("ProductMark", () => {
  it("renders the Ledgerly name and version", () => {
    render(<ProductMark />);

    expect(
      screen.getByRole("heading", { level: 1, name: "Ledgerly" }),
    ).toBeInTheDocument();
    expect(screen.getByText(`Version ${APP_VERSION}`)).toBeInTheDocument();
  });
});
