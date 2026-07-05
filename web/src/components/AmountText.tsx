import type { HTMLAttributes } from "react";

import { formatMinorUnits } from "@/components/amountFormat";
import { cx } from "@/components/utils";

export type AmountTextProps = HTMLAttributes<HTMLSpanElement> & {
  readonly amountMinor: number;
  readonly currency: string;
  readonly locale?: string;
};

export function AmountText({
  amountMinor,
  className,
  currency,
  locale,
  ...props
}: AmountTextProps) {
  return (
    <span className={cx("ui-amount-text", className)} {...props}>
      {formatMinorUnits({ amountMinor, currency, locale })}
    </span>
  );
}
