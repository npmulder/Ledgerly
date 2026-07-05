import type {
  HTMLAttributes,
  ReactNode,
  TdHTMLAttributes,
  ThHTMLAttributes,
} from "react";

import { cx } from "@/components/utils";

export type TableProps = HTMLAttributes<HTMLDivElement> & {
  readonly children: ReactNode;
};

export function Table({ children, className, ...props }: TableProps) {
  return (
    <div className={cx("ui-table-shell", className)} {...props}>
      <table className="ui-table">{children}</table>
    </div>
  );
}

export function TableHead({ children }: { readonly children: ReactNode }) {
  return <thead className="ui-table__head">{children}</thead>;
}

export function TableBody({ children }: { readonly children: ReactNode }) {
  return <tbody className="ui-table__body">{children}</tbody>;
}

export function TableFooter({ children }: { readonly children: ReactNode }) {
  return <tfoot className="ui-table__footer">{children}</tfoot>;
}

export type TableRowTone = "default" | "overdue";

export type TableRowProps = HTMLAttributes<HTMLTableRowElement> & {
  readonly children: ReactNode;
  readonly tone?: TableRowTone;
};

export function TableRow({
  children,
  className,
  tone = "default",
  ...props
}: TableRowProps) {
  return (
    <tr
      className={cx(
        "ui-table__row",
        tone !== "default" && `ui-table__row--${tone}`,
        className,
      )}
      {...props}
    >
      {children}
    </tr>
  );
}

type TableCellAlign = "left" | "right";

export type TableHeaderCellProps = ThHTMLAttributes<HTMLTableCellElement> & {
  readonly align?: TableCellAlign;
};

export function TableHeaderCell({
  align = "left",
  className,
  ...props
}: TableHeaderCellProps) {
  return (
    <th
      className={cx(
        "ui-table__header-cell",
        align === "right" && "ui-table__cell--right",
        className,
      )}
      {...props}
    />
  );
}

export type TableCellVariant = "default" | "mono" | "mono-numeric" | "numeric";

export type TableCellProps = TdHTMLAttributes<HTMLTableCellElement> & {
  readonly align?: TableCellAlign;
  readonly variant?: TableCellVariant;
};

export function TableCell({
  align = "left",
  className,
  variant = "default",
  ...props
}: TableCellProps) {
  return (
    <td
      className={cx(
        "ui-table__cell",
        align === "right" && "ui-table__cell--right",
        variant !== "default" && `ui-table__cell--${variant}`,
        className,
      )}
      {...props}
    />
  );
}
