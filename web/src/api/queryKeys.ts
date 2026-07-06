export type ApiQueryKey = readonly [
  module: string,
  resource: string,
  params: Record<string, unknown>,
];

export const queryKeys = {
  dla: {
    balance: (): ApiQueryKey => ["dla", "balance", {}],
    ledger: (cursor: string | null = null): ApiQueryKey => [
      "dla",
      "ledger",
      { cursor },
    ],
    ledgerPages: (): ApiQueryKey => ["dla", "ledgerPages", {}],
  },
  identity: {
    me: (): ApiQueryKey => ["identity", "me", {}],
    pats: (): ApiQueryKey => ["identity", "pats", {}],
    profile: (): ApiQueryKey => ["identity", "profile", {}],
  },
  invoicing: {
    clients: (includeArchived = false): ApiQueryKey => [
      "invoicing",
      "clients",
      { includeArchived },
    ],
  },
  jurisdiction: {
    pack: (): ApiQueryKey => ["jurisdiction", "pack", {}],
  },
  platform: {
    health: (): ApiQueryKey => ["platform", "healthz", {}],
  },
  reports: {
    calendar: (): ApiQueryKey => ["reports", "calendar", {}],
    pl: (from: string, to: string): ApiQueryKey => [
      "reports",
      "pl",
      { from, to },
    ],
    profitYTD: (taxYear: string): ApiQueryKey => [
      "reports",
      "profitYTD",
      { taxYear },
    ],
    vat: (period: string): ApiQueryKey => ["reports", "vat", { period }],
  },
} as const;
