export type ApiQueryKey = readonly [
  module: string,
  resource: string,
  params: Record<string, unknown>,
];

export type InvoicesQueryParams = {
  readonly limit?: number;
  readonly offset?: number;
  readonly search?: string;
  readonly status?: string;
};

export const queryKeys = {
  advisor: {
    insights: (surface: string): ApiQueryKey => [
      "advisor",
      "insights",
      { surface },
    ],
  },
  banking: {
    accounts: (): ApiQueryKey => ["banking", "accounts", {}],
    feed: (accountId: number | null = null): ApiQueryKey => [
      "banking",
      "feed",
      { accountId },
    ],
    recent: (limit = 10, accountId: number | null = null): ApiQueryKey => [
      "banking",
      "recent",
      { accountId, limit },
    ],
    payeeRules: (): ApiQueryKey => ["banking", "payeeRules", {}],
    review: (): ApiQueryKey => ["banking", "review", {}],
  },
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
  dividends: {
    documents: (id: string | null = null): ApiQueryKey => [
      "dividends",
      "documents",
      { id },
    ],
    headroom: (): ApiQueryKey => ["dividends", "headroom", {}],
    history: (): ApiQueryKey => ["dividends", "history", {}],
    validation: (amountMinor: number | null = null): ApiQueryKey => [
      "dividends",
      "validation",
      { amountMinor },
    ],
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
    invoices: ({
      limit = 50,
      offset = 0,
      search = "",
      status = "all",
    }: InvoicesQueryParams = {}): ApiQueryKey => [
      "invoicing",
      "invoices",
      { limit, offset, search, status },
    ],
  },
  jurisdiction: {
    pack: (): ApiQueryKey => ["jurisdiction", "pack", {}],
  },
  moneyfx: {
    todayRate: (from: string, to: string): ApiQueryKey => [
      "moneyfx",
      "todayRate",
      { from, to },
    ],
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
