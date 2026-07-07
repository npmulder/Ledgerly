import { type KeyboardEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { isApiError } from "@/api/client";
import {
  createExpenseAccount,
  getExpenseAccounts,
  type LedgerAccount,
  type LedgerAccountsResponse,
} from "@/api/ledger";
import { queryKeys } from "@/api/queryKeys";
import { Button, Field, Input, Select } from "@/components";

type ExpenseCategoryPickerProps = {
  readonly disabled?: boolean;
  readonly label: string;
  readonly name?: string;
  readonly onChange: (accountCode: string) => void;
  readonly required?: boolean;
  readonly value: string;
};

export function ExpenseCategoryPicker({
  disabled = false,
  label,
  name,
  onChange,
  required = false,
  value,
}: ExpenseCategoryPickerProps) {
  const queryClient = useQueryClient();
  const [isCreating, setIsCreating] = useState(false);
  const [newCode, setNewCode] = useState("");
  const [newName, setNewName] = useState("");
  const queryKey = queryKeys.ledger.expenseAccounts();
  const accountsQuery = useQuery({
    queryFn: getExpenseAccounts,
    queryKey,
  });
  const accounts = useMemo(
    () => sortExpenseAccounts(accountsQuery.data?.accounts ?? []),
    [accountsQuery.data?.accounts],
  );

  useEffect(() => {
    if (!value && accounts.length > 0) {
      onChange(accounts[0].code);
    }
  }, [accounts, onChange, value]);

  const createMutation = useMutation({
    mutationFn: createExpenseAccount,
    onSuccess: (account) => {
      queryClient.setQueryData<LedgerAccountsResponse>(queryKey, (current) => ({
        accounts: sortExpenseAccounts([
          ...(current?.accounts.filter((item) => item.code !== account.code) ??
            []),
          account,
        ]),
      }));
      void queryClient.invalidateQueries({ queryKey });
      onChange(account.code);
      setNewCode("");
      setNewName("");
      setIsCreating(false);
    },
  });

  function handleCreate() {
    const code = newCode.trim().toLowerCase();
    const accountName = newName.trim();
    if (!code || !accountName || createMutation.isPending) {
      return;
    }
    createMutation.mutate({ code, name: accountName });
  }

  function handleCreateKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Enter") {
      event.preventDefault();
      handleCreate();
    }
  }

  const selectDisabled =
    disabled ||
    (accounts.length === 0 &&
      (accountsQuery.isPending || accountsQuery.isError));
  const queryErrorMessage = accountsQuery.isError
    ? categoryProblemMessage(accountsQuery.error, "Unable to load categories")
    : null;
  const createErrorMessage = createMutation.isError
    ? categoryProblemMessage(createMutation.error, "Unable to create category")
    : null;

  return (
    <div className="expense-category-picker">
      <Field label={label}>
        <Select
          disabled={selectDisabled}
          name={name}
          onChange={(event) => onChange(event.target.value)}
          required={required}
          value={value}
        >
          {accountsQuery.isPending && accounts.length === 0 ? (
            <option value="">Loading</option>
          ) : null}
          {accountsQuery.isError && accounts.length === 0 ? (
            <option value="">Unavailable</option>
          ) : null}
          {!accountsQuery.isPending &&
          !accountsQuery.isError &&
          accounts.length === 0 ? (
            <option value="">No categories</option>
          ) : null}
          {accounts.map((account) => (
            <option key={account.code} value={account.code}>
              {account.name}
            </option>
          ))}
        </Select>
      </Field>

      {queryErrorMessage ? (
        <div className="problem-alert" role="alert">
          <strong>{queryErrorMessage}</strong>
        </div>
      ) : null}

      {isCreating ? (
        <div
          className="expense-category-picker__form"
          onKeyDown={handleCreateKeyDown}
        >
          <div className="expense-category-picker__fields">
            <Field label="Code">
              <Input
                autoComplete="off"
                onChange={(event) => setNewCode(event.target.value)}
                pattern="[0-9]{4}-[a-z0-9]+(-[a-z0-9]+)*"
                placeholder="5040-training"
                value={newCode}
              />
            </Field>
            <Field label="Name">
              <Input
                autoComplete="off"
                onChange={(event) => setNewName(event.target.value)}
                value={newName}
              />
            </Field>
          </div>
          {createErrorMessage ? (
            <div className="problem-alert" role="alert">
              <strong>{createErrorMessage}</strong>
            </div>
          ) : null}
          <div className="expense-category-picker__actions">
            <Button
              disabled={
                createMutation.isPending ||
                newCode.trim() === "" ||
                newName.trim() === ""
              }
              onClick={handleCreate}
              size="small"
              type="button"
            >
              {createMutation.isPending ? "Creating" : "Create"}
            </Button>
            <Button
              disabled={createMutation.isPending}
              onClick={() => {
                setIsCreating(false);
                createMutation.reset();
              }}
              size="small"
              type="button"
              variant="secondary"
            >
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <div className="expense-category-picker__actions">
          <Button
            disabled={disabled}
            onClick={() => setIsCreating(true)}
            size="small"
            type="button"
            variant="secondary"
          >
            New category
          </Button>
        </div>
      )}
    </div>
  );
}

function sortExpenseAccounts(accounts: readonly LedgerAccount[]) {
  return [...accounts].sort((left, right) =>
    left.code.localeCompare(right.code),
  );
}

function categoryProblemMessage(error: unknown, fallbackTitle: string) {
  if (isApiError(error)) {
    return error.problem.detail ?? error.problem.title;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return fallbackTitle;
}
