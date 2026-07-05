function getCurrencyFractionDigits(currency: string, locale: string) {
  return (
    new Intl.NumberFormat(locale, {
      currency,
      style: "currency",
    }).resolvedOptions().maximumFractionDigits ?? 2
  );
}

export function formatMinorUnits({
  amountMinor,
  currency,
  locale = "en-GB",
}: {
  readonly amountMinor: number;
  readonly currency: string;
  readonly locale?: string;
}) {
  const fractionDigits = getCurrencyFractionDigits(currency, locale);
  const majorAmount = amountMinor / 10 ** fractionDigits;

  return new Intl.NumberFormat(locale, {
    currency,
    maximumFractionDigits: fractionDigits,
    minimumFractionDigits: fractionDigits,
    style: "currency",
  }).format(majorAmount);
}
