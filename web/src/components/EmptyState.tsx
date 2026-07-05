import type { HTMLAttributes, ReactNode } from "react";

import { cx } from "@/components/utils";

export type EmptyStateProps = HTMLAttributes<HTMLElement> & {
  readonly children?: ReactNode;
  readonly icon?: ReactNode;
  readonly title?: ReactNode;
};

export function EmptyState({
  children = "Every transaction is coded. Next statement import suggested Friday.",
  className,
  icon = "✓",
  title = "All caught up",
  ...props
}: EmptyStateProps) {
  return (
    <section className={cx("ui-empty-state", className)} {...props}>
      <div className="ui-empty-state__icon" aria-hidden="true">
        {icon}
      </div>
      <h2 className="ui-empty-state__title">{title}</h2>
      <p className="ui-empty-state__copy">{children}</p>
    </section>
  );
}
