import { type FormEvent, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";

import {
  createBankingPayeeRule,
  deleteBankingPayeeRule,
  getBankingPayeeRules,
  updateBankingPayeeRule,
  type BankingPayeeRule,
  type BankingPayeeRuleRequest,
} from "@/api/banking";
import { isApiError } from "@/api/client";
import { queryKeys } from "@/api/queryKeys";
import {
  AuditHistoryPanel,
  Badge,
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  PageTitle,
  Select,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeaderCell,
  TableRow,
} from "@/components";
import { formatAccountCode, recodeAccounts } from "@/screens/bankingCategories";

type RuleDraft = BankingPayeeRuleRequest;
type ToastState = {
  message: string;
  tone: "error" | "success";
};

const defaultDraft: RuleDraft = {
  account_code: recodeAccounts[1].value,
  match_mode: "exact",
  matcher: "",
};

export function BankingPayeeRulesScreen() {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<RuleDraft>(defaultDraft);
  const [editingID, setEditingID] = useState<number | null>(null);
  const [editDraft, setEditDraft] = useState<RuleDraft>(defaultDraft);
  const [historyID, setHistoryID] = useState<number | null>(null);
  const [toast, setToast] = useState<ToastState | null>(null);

  const rulesQuery = useQuery({
    queryFn: getBankingPayeeRules,
    queryKey: queryKeys.banking.payeeRules(),
  });

  const createMutation = useMutation({
    mutationFn: createBankingPayeeRule,
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSuccess: async (created) => {
      setDraft(defaultDraft);
      setHistoryID(created.id);
      setToast({ message: "Payee rule created.", tone: "success" });
      await queryClient.invalidateQueries({
        queryKey: queryKeys.audit.history(
          "banking",
          "payee_rule",
          String(created.id),
        ),
      });
      await refreshPayeeRules(queryClient);
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, rule }: { id: number; rule: BankingPayeeRuleRequest }) =>
      updateBankingPayeeRule(id, rule),
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSuccess: async (updated) => {
      setEditingID(null);
      setHistoryID(updated.id);
      setToast({ message: "Payee rule updated.", tone: "success" });
      await queryClient.invalidateQueries({
        queryKey: queryKeys.audit.history(
          "banking",
          "payee_rule",
          String(updated.id),
        ),
      });
      await refreshPayeeRules(queryClient);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: deleteBankingPayeeRule,
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSuccess: async (_result, id) => {
      setToast({ message: "Payee rule deleted.", tone: "success" });
      await queryClient.invalidateQueries({
        queryKey: queryKeys.audit.history("banking", "payee_rule", String(id)),
      });
      await refreshPayeeRules(queryClient);
    },
  });

  const rules = rulesQuery.data?.rules ?? [];
  const historyRule = rules.find((rule) => rule.id === historyID);
  const isMutating =
    createMutation.isPending ||
    updateMutation.isPending ||
    deleteMutation.isPending;

  function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    createMutation.mutate(normalizeDraft(draft));
  }

  function startEdit(rule: BankingPayeeRule) {
    setEditingID(rule.id);
    setHistoryID(rule.id);
    setEditDraft(ruleToDraft(rule));
  }

  function saveEdit(ruleID: number) {
    updateMutation.mutate({ id: ruleID, rule: normalizeDraft(editDraft) });
  }

  function deleteRule(rule: BankingPayeeRule) {
    if (window.confirm(`Delete payee rule for ${rule.matcher}?`)) {
      deleteMutation.mutate(rule.id);
    }
  }

  return (
    <div className="banking-rules-screen">
      <div className="banking-screen__header">
        <div>
          <PageTitle>Payee rules</PageTitle>
          <p className="type-secondary">
            Review learned recodes and manage manual categorisation rules.
          </p>
        </div>
        <div className="banking-screen__actions">
          <Link
            className="ui-button ui-button--secondary ui-button--medium"
            to="/banking"
          >
            Banking queue
          </Link>
        </div>
      </div>

      {toast ? (
        <div
          className={`banking-toast banking-toast--${toast.tone}`}
          role={toast.tone === "error" ? "alert" : "status"}
        >
          {toast.message}
        </div>
      ) : null}

      {rulesQuery.isError ? (
        <ProblemAlert
          error={rulesQuery.error}
          fallbackTitle="Unable to load payee rules."
        />
      ) : null}

      <Card title="Manual rule">
        <form className="banking-rule-form" onSubmit={handleCreate}>
          <RuleDraftFields draft={draft} onChange={setDraft} />
          <Button disabled={createMutation.isPending} type="submit">
            {createMutation.isPending ? "Creating" : "Create rule"}
          </Button>
        </form>
      </Card>

      <section aria-label="Payee rules">
        {rulesQuery.isPending ? (
          <Card>
            <p className="type-secondary">Loading payee rules.</p>
          </Card>
        ) : null}

        {!rulesQuery.isPending && rules.length === 0 ? (
          <EmptyState title="No payee rules">
            Learned and manual rules will appear here.
          </EmptyState>
        ) : null}

        {rules.length > 0 ? (
          <Table className="banking-rules-table">
            <TableHead>
              <TableRow>
                <TableHeaderCell>Matcher</TableHeaderCell>
                <TableHeaderCell>Mode</TableHeaderCell>
                <TableHeaderCell>Category</TableHeaderCell>
                <TableHeaderCell align="right">Times applied</TableHeaderCell>
                <TableHeaderCell>Created from</TableHeaderCell>
                <TableHeaderCell>Actions</TableHeaderCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {rules.map((rule) =>
                editingID === rule.id ? (
                  <EditableRuleRow
                    busy={isMutating}
                    draft={editDraft}
                    key={rule.id}
                    onCancel={() => setEditingID(null)}
                    onChange={setEditDraft}
                    onSave={() => saveEdit(rule.id)}
                    rule={rule}
                  />
                ) : (
                  <ReadOnlyRuleRow
                    busy={isMutating}
                    key={rule.id}
                    onDelete={() => deleteRule(rule)}
                    onEdit={() => startEdit(rule)}
                    onHistory={() => setHistoryID(rule.id)}
                    rule={rule}
                  />
                ),
              )}
            </TableBody>
          </Table>
        ) : null}
      </section>

      {historyID ? (
        <AuditHistoryPanel
          entity="payee_rule"
          entityId={String(historyID)}
          module="banking"
          title={historyRule ? `History: ${historyRule.matcher}` : "History"}
        />
      ) : null}
    </div>
  );
}

function RuleDraftFields({
  draft,
  onChange,
}: {
  readonly draft: RuleDraft;
  readonly onChange: (draft: RuleDraft) => void;
}) {
  return (
    <>
      <Field label="Matcher">
        <Input
          onChange={(event) =>
            onChange({ ...draft, matcher: event.target.value })
          }
          required
          value={draft.matcher}
        />
      </Field>
      <Field label="Mode">
        <Select
          onChange={(event) =>
            onChange({
              ...draft,
              match_mode: event.target.value as RuleDraft["match_mode"],
            })
          }
          value={draft.match_mode}
        >
          <option value="exact">Exact</option>
          <option value="contains">Contains</option>
        </Select>
      </Field>
      <Field label="Category">
        <Select
          onChange={(event) =>
            onChange({ ...draft, account_code: event.target.value })
          }
          value={draft.account_code}
        >
          {recodeAccounts.map((account) => (
            <option key={account.value} value={account.value}>
              {account.label}
            </option>
          ))}
        </Select>
      </Field>
    </>
  );
}

function ReadOnlyRuleRow({
  busy,
  onDelete,
  onEdit,
  onHistory,
  rule,
}: {
  readonly busy: boolean;
  readonly onDelete: () => void;
  readonly onEdit: () => void;
  readonly onHistory: () => void;
  readonly rule: BankingPayeeRule;
}) {
  return (
    <TableRow>
      <TableCell variant="mono">{rule.matcher}</TableCell>
      <TableCell>
        <Badge variant="neutral">{formatMatchMode(rule.match_mode)}</Badge>
      </TableCell>
      <TableCell>{formatAccountCode(rule.account_code)}</TableCell>
      <TableCell align="right" variant="numeric">
        {rule.times_applied}
      </TableCell>
      <TableCell>{formatCreatedFrom(rule.created_from)}</TableCell>
      <TableCell>
        <div className="banking-rule-actions">
          <Button
            disabled={busy}
            onClick={onEdit}
            size="small"
            type="button"
            variant="secondary"
          >
            Edit
          </Button>
          <Button
            disabled={busy}
            onClick={onHistory}
            size="small"
            type="button"
            variant="secondary"
          >
            History
          </Button>
          <Button
            disabled={busy}
            onClick={onDelete}
            size="small"
            type="button"
            variant="danger"
          >
            Delete
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function EditableRuleRow({
  busy,
  draft,
  onCancel,
  onChange,
  onSave,
  rule,
}: {
  readonly busy: boolean;
  readonly draft: RuleDraft;
  readonly onCancel: () => void;
  readonly onChange: (draft: RuleDraft) => void;
  readonly onSave: () => void;
  readonly rule: BankingPayeeRule;
}) {
  return (
    <TableRow>
      <TableCell>
        <Input
          aria-label={`Matcher for ${rule.matcher}`}
          onChange={(event) =>
            onChange({ ...draft, matcher: event.target.value })
          }
          required
          value={draft.matcher}
        />
      </TableCell>
      <TableCell>
        <Select
          aria-label={`Mode for ${rule.matcher}`}
          onChange={(event) =>
            onChange({
              ...draft,
              match_mode: event.target.value as RuleDraft["match_mode"],
            })
          }
          value={draft.match_mode}
        >
          <option value="exact">Exact</option>
          <option value="contains">Contains</option>
        </Select>
      </TableCell>
      <TableCell>
        <Select
          aria-label={`Category for ${rule.matcher}`}
          onChange={(event) =>
            onChange({ ...draft, account_code: event.target.value })
          }
          value={draft.account_code}
        >
          {recodeAccounts.map((account) => (
            <option key={account.value} value={account.value}>
              {account.label}
            </option>
          ))}
        </Select>
      </TableCell>
      <TableCell align="right" variant="numeric">
        {rule.times_applied}
      </TableCell>
      <TableCell>{formatCreatedFrom(rule.created_from)}</TableCell>
      <TableCell>
        <div className="banking-rule-actions">
          <Button
            disabled={busy || draft.matcher.trim() === ""}
            onClick={onSave}
            size="small"
            type="button"
          >
            Save
          </Button>
          <Button
            disabled={busy}
            onClick={onCancel}
            size="small"
            type="button"
            variant="secondary"
          >
            Cancel
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function ProblemAlert({
  error,
  fallbackTitle,
}: {
  readonly error: unknown;
  readonly fallbackTitle: string;
}) {
  const problem = isApiError(error) ? error.problem : null;
  return (
    <div className="problem-alert" role="alert">
      <strong>{problem?.title ?? fallbackTitle}</strong>
      {problem?.detail ? <span>{problem.detail}</span> : null}
    </div>
  );
}

async function refreshPayeeRules(
  queryClient: ReturnType<typeof useQueryClient>,
) {
  await queryClient.invalidateQueries({
    queryKey: queryKeys.banking.payeeRules(),
  });
  await queryClient.invalidateQueries({ queryKey: queryKeys.banking.review() });
}

function normalizeDraft(draft: RuleDraft): RuleDraft {
  return {
    ...draft,
    matcher: draft.matcher.trim(),
  };
}

function ruleToDraft(rule: BankingPayeeRule): RuleDraft {
  return {
    account_code: rule.account_code,
    match_mode: rule.match_mode,
    matcher: rule.matcher,
  };
}

function problemMessage(error: unknown) {
  if (isApiError(error)) {
    return error.problem.detail ?? error.problem.title;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return "Banking request failed.";
}

function formatMatchMode(value: BankingPayeeRule["match_mode"]) {
  return value === "contains" ? "Contains" : "Exact";
}

function formatCreatedFrom(value: BankingPayeeRule["created_from"]) {
  return value === "recode" ? "Recode" : "Manual";
}
