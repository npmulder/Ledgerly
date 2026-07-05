import {
  MutationCache,
  QueryCache,
  QueryClient,
} from "@tanstack/react-query";

import { isApiError } from "@/api/client";
import { notifyUnexpectedApiError } from "@/api/errorReporter";

const STALE_TIME_MS = 60_000;
const RETRY_LIMIT = 2;

export function retryApiFailure(failureCount: number, error: unknown) {
  if (isApiError(error) && error.isClientError) {
    return false;
  }

  return failureCount < RETRY_LIMIT;
}

export const queryClient = new QueryClient({
  defaultOptions: {
    mutations: {
      retry: retryApiFailure,
    },
    queries: {
      retry: retryApiFailure,
      staleTime: STALE_TIME_MS,
    },
  },
  mutationCache: new MutationCache({
    onError: notifyUnexpectedApiError,
  }),
  queryCache: new QueryCache({
    onError: notifyUnexpectedApiError,
  }),
});
