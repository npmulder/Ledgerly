import { useQuery } from "@tanstack/react-query";

import { getAuditHistory, type AuditEntry } from "@/api/audit";
import { isApiError } from "@/api/client";
import { queryKeys } from "@/api/queryKeys";

export type AuditHistoryPanelProps = {
  readonly entity: string;
  readonly entityId: string | null | undefined;
  readonly module: string;
  readonly title?: string;
};

export function AuditHistoryPanel({
  entity,
  entityId,
  module,
  title = "History",
}: AuditHistoryPanelProps) {
  const normalizedEntityId = entityId?.trim() ?? "";
  const historyQuery = useQuery({
    enabled: normalizedEntityId !== "",
    queryFn: () =>
      getAuditHistory({
        entity,
        entityId: normalizedEntityId,
        module,
      }),
    queryKey: queryKeys.audit.history(module, entity, normalizedEntityId),
  });
  const entries = historyQuery.data?.entries ?? [];

  return (
    <section className="audit-history-panel" aria-label={title}>
      <div className="audit-history-panel__header">
        <h2>{title}</h2>
        <span>{entries.length}</span>
      </div>

      {historyQuery.isPending && normalizedEntityId !== "" ? (
        <p className="type-secondary">Loading history.</p>
      ) : null}

      {historyQuery.isError ? (
        <p className="audit-history-panel__error" role="alert">
          {problemMessage(historyQuery.error)}
        </p>
      ) : null}

      {!historyQuery.isPending &&
      !historyQuery.isError &&
      entries.length === 0 ? (
        <p className="type-secondary">No history yet.</p>
      ) : null}

      {entries.length > 0 ? (
        <ol className="audit-history-list">
          {entries.map((entry) => (
            <li className="audit-history-list__item" key={entry.id}>
              <div className="audit-history-list__meta">
                <time dateTime={entry.occurred_at}>
                  {formatTimestamp(entry.occurred_at)}
                </time>
                <span>{entry.actor}</span>
              </div>
              <dl className="audit-history-diff">
                {Object.entries(entry.diff).map(([field, change]) => (
                  <div className="audit-history-diff__row" key={field}>
                    <dt>{formatField(field)}</dt>
                    <dd>
                      <span>{formatValue(change.before)}</span>
                      <span aria-hidden="true">-&gt;</span>
                      <span>{formatValue(change.after)}</span>
                    </dd>
                  </div>
                ))}
              </dl>
            </li>
          ))}
        </ol>
      ) : null}
    </section>
  );
}

function problemMessage(error: unknown) {
  if (isApiError(error)) {
    return error.problem.detail ?? error.problem.title;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "Unable to load history.";
}

function formatTimestamp(value: AuditEntry["occurred_at"]) {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) {
    return value;
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

function formatField(value: string) {
  return value.replaceAll("_", " ");
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined || value === "") {
    return "empty";
  }
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return JSON.stringify(value);
}
