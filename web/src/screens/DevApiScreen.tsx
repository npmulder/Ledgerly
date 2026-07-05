import { useQuery } from "@tanstack/react-query";

import { apiGet, isApiError } from "@/api/client";
import { queryKeys } from "@/api/queryKeys";

export function DevApiScreen() {
  const healthQuery = useQuery({
    queryFn: fetchHealth,
    queryKey: queryKeys.platform.health(),
  });

  const checks = Object.entries(healthQuery.data?.checks ?? {});

  return (
    <main className="dev-api-screen">
      <header className="dev-api-header">
        <div>
          <p className="eyebrow">Developer API</p>
          <h1>API Health</h1>
        </div>
        <button
          className="secondary-action"
          disabled={healthQuery.isFetching}
          onClick={() => {
            void healthQuery.refetch();
          }}
          type="button"
        >
          Refresh
        </button>
      </header>

      <section aria-live="polite" className="health-summary">
        <div>
          <span>Status</span>
          <strong>{statusLabel(healthQuery)}</strong>
        </div>
        <div>
          <span>Version</span>
          <strong>{versionLabel(healthQuery)}</strong>
        </div>
      </section>

      {healthQuery.isError ? (
        <p className="health-message" role="status">
          {errorLabel(healthQuery.error)}
        </p>
      ) : null}

      <section className="checks-section">
        <h2>Checks</h2>
        <table>
          <thead>
            <tr>
              <th>Check</th>
              <th>Status</th>
              <th>Detail</th>
            </tr>
          </thead>
          <tbody>
            {checks.length > 0 ? (
              checks.map(([name, check]) => (
                <tr key={name}>
                  <td>{name}</td>
                  <td>{check.status}</td>
                  <td>{check.error ?? "ok"}</td>
                </tr>
              ))
            ) : (
              <tr>
                <td colSpan={3}>No checks reported</td>
              </tr>
            )}
          </tbody>
        </table>
      </section>
    </main>
  );
}

type HealthData = Awaited<ReturnType<typeof fetchHealth>>;

type HealthQueryState = {
  data: HealthData | undefined;
  error: unknown;
  isPending: boolean;
};

function fetchHealth({ signal }: { signal?: AbortSignal }) {
  return apiGet("/healthz", { signal });
}

function statusLabel(query: HealthQueryState) {
  if (query.isPending) {
    return "Loading";
  }

  if (query.data) {
    return query.data.status;
  }

  if (isApiError(query.error)) {
    return `${query.error.status} ${query.error.problem.title}`;
  }

  return "Unavailable";
}

function versionLabel(query: HealthQueryState) {
  if (query.data) {
    return query.data.version;
  }

  if (isApiError(query.error)) {
    const version = query.error.problem.version;
    return typeof version === "string" ? version : "unknown";
  }

  return query.isPending ? "Loading" : "unknown";
}

function errorLabel(error: unknown) {
  if (isApiError(error)) {
    return error.problem.detail ?? error.problem.title;
  }

  if (error instanceof Error) {
    return error.message;
  }

  return "Request failed";
}
