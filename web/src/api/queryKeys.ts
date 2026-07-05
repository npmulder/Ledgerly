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
  platform: {
    health: (): ApiQueryKey => ["platform", "healthz", {}],
  },
} as const;
