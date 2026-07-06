export type ApiQueryKey = readonly [
  module: string,
  resource: string,
  params: Record<string, unknown>,
];

export const queryKeys = {
  dashboard: {
    summary: (): ApiQueryKey => ["dashboard", "summary", {}],
  },
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
    invoice: (id: string): ApiQueryKey => ["invoicing", "invoice", { id }],
    invoices: (): ApiQueryKey => ["invoicing", "invoices", {}],
  },
  jurisdiction: {
    pack: (): ApiQueryKey => ["jurisdiction", "pack", {}],
  },
  platform: {
    health: (): ApiQueryKey => ["platform", "healthz", {}],
  },
} as const;
