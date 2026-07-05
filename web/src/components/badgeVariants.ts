export type BadgeVariant =
  "count" | "draft" | "neutral" | "overdue" | "paid" | "sent";

export const badgeVariantClassNames: Record<BadgeVariant, string> = {
  count: "ui-badge--count",
  draft: "ui-badge--draft",
  neutral: "ui-badge--neutral",
  overdue: "ui-badge--overdue",
  paid: "ui-badge--paid",
  sent: "ui-badge--sent",
};

export function getBadgeVariantClassName(variant: BadgeVariant) {
  return badgeVariantClassNames[variant];
}
