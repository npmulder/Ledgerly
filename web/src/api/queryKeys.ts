export type ApiQueryKey = readonly [
  module: string,
  resource: string,
  params: Record<string, unknown>,
];

export const queryKeys = {
  identity: {
    me: (): ApiQueryKey => ["identity", "me", {}],
    profile: (): ApiQueryKey => ["identity", "profile", {}],
  },
  invoicing: {
    clients: (includeArchived = false): ApiQueryKey => [
      "invoicing",
      "clients",
      { includeArchived },
    ],
  },
  platform: {
    health: (): ApiQueryKey => ["platform", "healthz", {}],
  },
} as const;
