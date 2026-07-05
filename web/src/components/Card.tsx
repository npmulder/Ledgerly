import type { HTMLAttributes, ReactNode } from "react";

import { cx } from "@/components/utils";

type CardElement = "article" | "div" | "section";

export type CardProps = HTMLAttributes<HTMLElement> & {
  readonly actions?: ReactNode;
  readonly as?: CardElement;
  readonly children: ReactNode;
  readonly footer?: ReactNode;
  readonly title?: ReactNode;
};

export function Card({
  actions,
  as: Component = "section",
  children,
  className,
  footer,
  title,
  ...props
}: CardProps) {
  return (
    <Component className={cx("ui-card", className)} {...props}>
      {(title || actions) && (
        <div className="ui-card__header">
          {title && <div className="ui-card__title">{title}</div>}
          {actions && <div className="ui-card__actions">{actions}</div>}
        </div>
      )}
      <div className="ui-card__body">{children}</div>
      {footer && <div className="ui-card__footer">{footer}</div>}
    </Component>
  );
}

export type PanelVariant = "default" | "advisor";

export type PanelProps = HTMLAttributes<HTMLElement> & {
  readonly children: ReactNode;
  readonly eyebrow?: ReactNode;
  readonly title?: ReactNode;
  readonly variant?: PanelVariant;
};

export function Panel({
  children,
  className,
  eyebrow,
  title,
  variant = "default",
  ...props
}: PanelProps) {
  return (
    <section
      className={cx("ui-panel", `ui-panel--${variant}`, className)}
      {...props}
    >
      {(eyebrow || title) && (
        <div className="ui-panel__header">
          {eyebrow && <p className="ui-panel__eyebrow">{eyebrow}</p>}
          {title && <h2 className="ui-panel__title">{title}</h2>}
        </div>
      )}
      <div className="ui-panel__body">{children}</div>
    </section>
  );
}
