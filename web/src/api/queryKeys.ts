export type ApiQueryKey = readonly [
  module: string,
  resource: string,
  params: Record<string, unknown>,
];

export const queryKeys = {
  platform: {
    health: (): ApiQueryKey => ["platform", "healthz", {}],
  },
} as const;
