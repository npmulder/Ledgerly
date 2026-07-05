import type { HTMLAttributes, ReactNode } from "react";

import type { BadgeVariant } from "@/components/badgeVariants";
import { getBadgeVariantClassName } from "@/components/badgeVariants";
import { cx } from "@/components/utils";

const defaultBadgeLabels: Record<BadgeVariant, string> = {
  count: "0",
  draft: "DRAFT",
  neutral: "NEW",
  overdue: "OVERDUE",
  paid: "PAID",
  sent: "SENT",
};

export type BadgeProps = HTMLAttributes<HTMLSpanElement> & {
  readonly children?: ReactNode;
  readonly daysOverdue?: number;
  readonly variant: BadgeVariant;
};

export function Badge({
  children,
  className,
  daysOverdue,
  variant,
  ...props
}: BadgeProps) {
  const label =
    children ??
    (variant === "overdue" && typeof daysOverdue === "number"
      ? `OVERDUE ${daysOverdue}D`
      : defaultBadgeLabels[variant]);

  return (
    <span
      className={cx("ui-badge", getBadgeVariantClassName(variant), className)}
      {...props}
    >
      {label}
    </span>
  );
}

export type PillVariant = "active" | "danger" | "default";

export type PillProps = HTMLAttributes<HTMLSpanElement> & {
  readonly children: ReactNode;
  readonly count?: number;
  readonly variant?: PillVariant;
};

export function Pill({
  children,
  className,
  count,
  variant = "default",
  ...props
}: PillProps) {
  return (
    <span
      className={cx("ui-pill", `ui-pill--${variant}`, className)}
      {...props}
    >
      <span>{children}</span>
      {typeof count === "number" && <Badge variant="count">{count}</Badge>}
    </span>
  );
}
