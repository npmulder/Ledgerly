import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";

import {
  dismissAdvisorInsight,
  getAdvisorInsights,
  refreshAdvisor,
  type AdvisorInsight,
  type AdvisorInsightsResponse,
  type AdvisorSurface,
} from "@/api/advisor";
import { isApiError } from "@/api/client";
import { sendInvoiceReminder } from "@/api/invoicing";
import { getTodayRate } from "@/api/moneyfx";
import { queryKeys } from "@/api/queryKeys";
import { Button } from "@/components/Button";
import { cx } from "@/components/utils";

const severityRank = {
  amber: 0,
  teal: 1,
} satisfies Record<AdvisorInsight["severity"], number>;

export type AdvisorPanelProps = {
  readonly maxInsights?: number;
  readonly surface?: AdvisorSurface;
};

export type AdvisorStripProps = {
  readonly surface: AdvisorSurface;
};

export function AdvisorPanel({
  maxInsights = 4,
  surface = "dashboard",
}: AdvisorPanelProps = {}) {
  const insightsQuery = useAdvisorInsights(surface);
  const refreshMutation = useRefreshAdvisor();
  const dismissMutation = useDismissAdvisorInsight(surface);
  const insights = insightsQuery.insights.slice(0, maxInsights);

  return (
    <section
      aria-label="Advisor panel"
      className="advisor-panel"
      data-surface={surface}
    >
      <div className="advisor-panel__header">
        <div>
          <p className="advisor-panel__eyebrow">Advisor</p>
          <h2 className="advisor-panel__title">Isle of Man rules</h2>
        </div>
        <AdvisorOverflow
          isRefreshing={refreshMutation.isPending}
          onRefresh={() => refreshMutation.mutate()}
        />
      </div>
      <div className="advisor-panel__body">
        {insightsQuery.isPending ? (
          <p className="advisor-panel__empty">Loading advisor.</p>
        ) : null}
        {!insightsQuery.isPending && insights.length === 0 ? (
          <p className="advisor-panel__empty">No insights — all caught up</p>
        ) : null}
        {insights.map((insight) => (
          <AdvisorInsightItem
            insight={insight}
            isDismissing={
              dismissMutation.isPending &&
              dismissMutation.variables === insight.key
            }
            key={insight.key}
            onDismiss={() => dismissMutation.mutate(insight.key)}
            variant="panel"
          />
        ))}
      </div>
    </section>
  );
}

export function AdvisorStrip({ surface }: AdvisorStripProps) {
  const insightsQuery = useAdvisorInsights(surface);
  const refreshMutation = useRefreshAdvisor();
  const dismissMutation = useDismissAdvisorInsight(surface);
  const insight = insightsQuery.insights[0];

  if (!insight) {
    return null;
  }

  return (
    <section
      aria-label={`${surfaceLabel(surface)} advisor`}
      className="advisor-strip"
      data-surface={surface}
    >
      <AdvisorInsightItem
        insight={insight}
        isDismissing={
          dismissMutation.isPending && dismissMutation.variables === insight.key
        }
        onDismiss={() => dismissMutation.mutate(insight.key)}
        variant="strip"
      />
      <AdvisorOverflow
        className="advisor-strip__overflow"
        isRefreshing={refreshMutation.isPending}
        onRefresh={() => refreshMutation.mutate()}
      />
    </section>
  );
}

function sortAdvisorInsights(
  insights: readonly AdvisorInsight[],
): AdvisorInsight[] {
  return [...insights].sort((left, right) => {
    const severityDelta =
      severityRank[left.severity] - severityRank[right.severity];
    if (severityDelta !== 0) {
      return severityDelta;
    }
    const recencyDelta =
      new Date(right.created_at).getTime() -
      new Date(left.created_at).getTime();
    if (recencyDelta !== 0) {
      return recencyDelta;
    }
    return left.key.localeCompare(right.key);
  });
}

function useAdvisorInsights(surface: AdvisorSurface) {
  const query = useQuery({
    queryFn: () => getAdvisorInsights(surface),
    queryKey: queryKeys.advisor.insights(surface),
  });
  return {
    ...query,
    insights: useMemo(
      () => sortAdvisorInsights(query.data?.insights ?? []),
      [query.data?.insights],
    ),
  };
}

function useDismissAdvisorInsight(surface: AdvisorSurface) {
  const queryClient = useQueryClient();
  const queryKey = queryKeys.advisor.insights(surface);

  return useMutation<
    unknown,
    Error,
    string,
    { previous?: AdvisorInsightsResponse }
  >({
    mutationFn: dismissAdvisorInsight,
    onError: (_error, _key, context) => {
      if (context?.previous) {
        queryClient.setQueryData(queryKey, context.previous);
      }
    },
    onMutate: async (key) => {
      await queryClient.cancelQueries({ queryKey });
      const previous =
        queryClient.getQueryData<AdvisorInsightsResponse>(queryKey);
      if (previous) {
        queryClient.setQueryData<AdvisorInsightsResponse>(queryKey, {
          insights: previous.insights.filter((insight) => insight.key !== key),
        });
      }
      return { previous };
    },
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: ["advisor"] });
    },
  });
}

function useRefreshAdvisor() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: refreshAdvisor,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["advisor"] });
    },
  });
}

function AdvisorInsightItem({
  insight,
  isDismissing,
  onDismiss,
  variant,
}: {
  readonly insight: AdvisorInsight;
  readonly isDismissing: boolean;
  readonly onDismiss: () => void;
  readonly variant: "panel" | "strip";
}) {
  const cta = useAdvisorCTA(insight);

  return (
    <div className={`advisor-insight advisor-insight--${variant}`}>
      <div className="advisor-insight-row" data-severity={insight.severity}>
        <span aria-hidden="true" className="advisor-insight-row__severity" />
        <span className="advisor-insight-row__text">
          {insight.rendered_text}
        </span>
        {cta ? (
          <Button
            disabled={cta.isPending}
            onClick={cta.onClick}
            size="small"
            type="button"
            variant={variant === "panel" ? "secondary" : "primary"}
          >
            {cta.isPending ? cta.pendingLabel : cta.label}
          </Button>
        ) : null}
        <Button
          aria-label={`Dismiss advisor insight: ${insight.rendered_text}`}
          className="advisor-insight-row__dismiss"
          disabled={isDismissing}
          onClick={onDismiss}
          size="small"
          type="button"
          variant="secondary"
        >
          ×
        </Button>
      </div>
      {cta?.message ? (
        <p className="advisor-insight__toast" role="status">
          {cta.message}
        </p>
      ) : null}
      {cta?.error ? (
        <p className="advisor-insight__error" role="alert">
          {cta.error}
        </p>
      ) : null}
    </div>
  );
}

function useAdvisorCTA(insight: AdvisorInsight) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const invoiceID =
    stringParam(insight.cta.params, "invoice_id") ??
    stringParam(insight.cta.params, "id");
  const reminderMutation = useMutation({
    mutationFn: (id: string) => sendInvoiceReminder(id),
    onError: (cause) => {
      setMessage("");
      setError(problemMessage(cause, "Unable to send reminder"));
    },
    onSuccess: (result) => {
      const number = result.invoice.number ?? "invoice";
      setError("");
      setMessage(`Reminder sent for ${number}.`);
      queryClient.setQueryData(
        queryKeys.invoicing.invoice(result.invoice.id),
        result.invoice,
      );
      void queryClient.invalidateQueries({
        queryKey: ["invoicing", "invoices"],
      });
      void queryClient.invalidateQueries({ queryKey: ["advisor"] });
    },
  });
  const ratesMutation = useMutation({
    mutationFn: async () => {
      const rate = await getTodayRate("EUR", "GBP");
      await refreshAdvisor();
      return rate;
    },
    onError: (cause) => {
      setMessage("");
      setError(problemMessage(cause, "Unable to refresh rates"));
    },
    onSuccess: (rate) => {
      setError("");
      setMessage("Rates checked.");
      queryClient.setQueryData(queryKeys.moneyfx.todayRate("EUR", "GBP"), rate);
      void queryClient.invalidateQueries({ queryKey: ["advisor"] });
      void queryClient.invalidateQueries({
        queryKey: queryKeys.dashboard.summary(),
      });
      void queryClient.invalidateQueries({ queryKey: ["moneyfx"] });
    },
  });

  if (insight.cta.action === "invoicing.sendReminder" && invoiceID) {
    return {
      error,
      isPending: reminderMutation.isPending,
      label: insight.cta.label,
      message,
      onClick: () => reminderMutation.mutate(invoiceID),
      pendingLabel: "Sending",
    };
  }

  if (insight.cta.action.startsWith("navigate:")) {
    return {
      error: "",
      isPending: false,
      label: insight.cta.label,
      message: "",
      onClick: () =>
        navigate(normalizeAdvisorRoute(insight.cta.action.slice(9))),
      pendingLabel: insight.cta.label,
    };
  }

  const route = routeForKnownAdvisorAction(insight.cta.action);
  if (route) {
    return {
      error: "",
      isPending: false,
      label: insight.cta.label,
      message: "",
      onClick: () => navigate(route),
      pendingLabel: insight.cta.label,
    };
  }

  if (insight.cta.action === "moneyfx.refreshRates") {
    return {
      error,
      isPending: ratesMutation.isPending,
      label: insight.cta.label,
      message,
      onClick: () => ratesMutation.mutate(),
      pendingLabel: "Refreshing",
    };
  }

  return null;
}

function routeForKnownAdvisorAction(action: string) {
  switch (action) {
    case "reports.openFilingCalendar":
      return "/reports";
    case "dividends.open":
      return "/dividends";
    default:
      return null;
  }
}

function AdvisorOverflow({
  className,
  isRefreshing,
  onRefresh,
}: {
  readonly className?: string;
  readonly isRefreshing: boolean;
  readonly onRefresh: () => void;
}) {
  const [open, setOpen] = useState(false);

  return (
    <div className={cx("advisor-overflow", className)}>
      <Button
        aria-expanded={open}
        aria-haspopup="menu"
        aria-label="Advisor actions"
        className="advisor-overflow__trigger"
        onClick={() => setOpen((current) => !current)}
        size="small"
        type="button"
        variant="secondary"
      >
        ⋯
      </Button>
      {open ? (
        <div className="advisor-overflow__menu" role="menu">
          <button
            disabled={isRefreshing}
            onClick={() => {
              onRefresh();
              setOpen(false);
            }}
            role="menuitem"
            type="button"
          >
            {isRefreshing ? "Refreshing" : "Refresh insights"}
          </button>
        </div>
      ) : null}
    </div>
  );
}

function surfaceLabel(surface: AdvisorSurface) {
  switch (surface) {
    case "dla":
      return "DLA";
    default:
      return `${surface[0].toUpperCase()}${surface.slice(1)}`;
  }
}

function stringParam(
  params: AdvisorInsight["cta"]["params"],
  key: string,
): string | null {
  const value = params?.[key];
  return typeof value === "string" && value.trim() ? value : null;
}

function normalizeAdvisorRoute(route: string) {
  const trimmed = route.trim();
  if (!trimmed.startsWith("/")) {
    return trimmed;
  }

  const url = new URL(trimmed, "https://ledgerly.local");
  if (url.pathname === "/dividends") {
    const amount = url.searchParams.get("amount");
    if (amount && /^\d+$/.test(amount)) {
      url.searchParams.set("amount", (Number(amount) / 100).toFixed(2));
    }
  }
  return `${url.pathname}${url.search}${url.hash}`;
}

function problemMessage(error: unknown, fallbackTitle: string) {
  if (isApiError(error)) {
    return error.problem.detail
      ? `${error.problem.title}: ${error.problem.detail}`
      : error.problem.title;
  }
  return fallbackTitle;
}
