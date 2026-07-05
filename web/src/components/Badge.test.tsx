import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Badge } from "@/components/Badge";
import {
  badgeVariantClassNames,
  getBadgeVariantClassName,
} from "@/components/badgeVariants";

describe("Badge", () => {
  it("maps every status variant to its class name", () => {
    expect(getBadgeVariantClassName("draft")).toBe(
      badgeVariantClassNames.draft,
    );
    expect(getBadgeVariantClassName("sent")).toBe(badgeVariantClassNames.sent);
    expect(getBadgeVariantClassName("paid")).toBe(badgeVariantClassNames.paid);
    expect(getBadgeVariantClassName("overdue")).toBe(
      badgeVariantClassNames.overdue,
    );
  });

  it("renders overdue day counts in the badge label", () => {
    render(<Badge daysOverdue={9} variant="overdue" />);

    expect(screen.getByText("OVERDUE 9D")).toHaveClass("ui-badge--overdue");
  });
});
