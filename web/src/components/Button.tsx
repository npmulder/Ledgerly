import type { ButtonHTMLAttributes } from "react";

import { cx } from "@/components/utils";

export type ButtonVariant = "danger" | "primary" | "secondary";
export type ButtonSize = "medium" | "small";

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  readonly size?: ButtonSize;
  readonly variant?: ButtonVariant;
};

export function Button({
  className,
  size = "medium",
  type = "button",
  variant = "primary",
  ...props
}: ButtonProps) {
  return (
    <button
      className={cx(
        "ui-button",
        `ui-button--${variant}`,
        `ui-button--${size}`,
        className,
      )}
      type={type}
      {...props}
    />
  );
}
