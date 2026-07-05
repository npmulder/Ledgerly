import type { HTMLAttributes, ReactNode } from "react";

import { cx } from "@/components/utils";

export type StatCardProps = HTMLAttributes<HTMLElement> & {
  readonly label: ReactNode;
  readonly secondary?: ReactNode;
  readonly value: ReactNode;
};

export function StatCard({
  className,
  label,
  secondary,
  value,
  ...props
}: StatCardProps) {
  return (
    <article className={cx("ui-stat-card", className)} {...props}>
      <p className="ui-stat-card__label">{label}</p>
      <p className="ui-stat-card__value">{value}</p>
      {secondary && <div className="ui-stat-card__secondary">{secondary}</div>}
    </article>
  );
}
