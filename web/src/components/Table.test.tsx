import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
} from "@/components/Table";

describe("Table", () => {
  it("applies the overdue row tint class", () => {
    render(
      <Table aria-label="Invoices">
        <TableHead>
          <TableRow>
            <TableHeaderCell>Number</TableHeaderCell>
          </TableRow>
        </TableHead>
        <TableBody>
          <TableRow tone="overdue">
            <TableCell>INV-2026-F2</TableCell>
          </TableRow>
        </TableBody>
      </Table>,
    );

    expect(screen.getByText("INV-2026-F2").closest("tr")).toHaveClass(
      "ui-table__row--overdue",
    );
  });
});
