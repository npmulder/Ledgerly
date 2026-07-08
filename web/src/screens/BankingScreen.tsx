import {
  type ChangeEvent,
  type FormEvent,
  useMemo,
  useRef,
  useState,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";

import {
  attachBankingReceipt,
  confirmBankingMatch,
  createBankingAccount,
  deleteBankingReceipt,
  excludeBankingTransaction,
  fileBankingTransactionToDLA,
  getBankingAccounts,
  getBankingFeed,
  getBankingInvoiceCandidates,
  getBankingReviewQueue,
  getRecentlyReconciled,
  importBankingCSV,
  recodeBankingTransaction,
  type BankingAccount,
  type BankingAccountsResponse,
  type BankingCommandResponse,
  type BankingCreateAccountRequest,
  type BankingInvoiceCandidate,
  type BankingMoney,
  type BankingRecentTransaction,
  type BankingReviewCard,
  type BankingReviewQueue,
  type BankingTransaction,
} from "@/api/banking";
import { isApiError } from "@/api/client";
import { getIdentityProfile } from "@/api/identity";
import { queryKeys } from "@/api/queryKeys";
import {
  AdvisorStrip,
  Badge,
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  PageTitle,
  Select,
  SplitMain,
  formatMinorUnits,
} from "@/components";
import { formatConfidence } from "@/screens/bankingFormat";
import { formatAccountCode } from "@/screens/bankingCategories";
import {
  ExpenseCategoryPicker,
  defaultExpenseAccountCode,
} from "@/screens/ExpenseCategoryPicker";

const recentLimit = 8;
const receiptAccept = "application/pdf,image/png,image/jpeg";

const defaultAccountDraft = (): BankingCreateAccountRequest => ({
  currency: "GBP",
  name: "",
  provider: "revolut",
});

type ToastState = {
  message: string;
  tone: "error" | "success";
};

type ExcludeVariables = {
  card: BankingReviewCard;
  reason: string;
};

type ExcludeContext = {
  previousQueue?: BankingReviewQueue;
};

type BankingDirector = {
  id: string;
  name: string;
};

type RecentKindByID = Partial<Record<number, BankingReviewCard["kind"]>>;

type ConfirmVariables = {
  invoiceID?: string;
  transactionID: number;
};

type AttachReceiptVariables = {
  file: File;
  transactionID: number;
};

type DeleteReceiptVariables = {
  transactionID: number;
};

export function BankingScreen() {
  const queryClient = useQueryClient();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [selectedAccountID, setSelectedAccountID] = useState<number | null>(
    null,
  );
  const [isCreateAccountOpen, setCreateAccountOpen] = useState(false);
  const [accountDraft, setAccountDraft] =
    useState<BankingCreateAccountRequest>(defaultAccountDraft);
  const [accountFormError, setAccountFormError] = useState<string | null>(null);
  const [recentKinds, setRecentKinds] = useState<RecentKindByID>({});
  const [toast, setToast] = useState<ToastState | null>(null);

  const accountsQuery = useQuery({
    queryFn: getBankingAccounts,
    queryKey: queryKeys.banking.accounts(),
  });
  const reviewQuery = useQuery({
    queryFn: getBankingReviewQueue,
    queryKey: queryKeys.banking.review(),
  });
  const profileQuery = useQuery({
    queryFn: getIdentityProfile,
    queryKey: queryKeys.identity.profile(),
  });
  const directors = useMemo(
    () => profileDirectors(profileQuery.data?.directors),
    [profileQuery.data?.directors],
  );
  const accounts = accountsQuery.data?.accounts ?? [];
  const selectedAccount = selectedAccountID
    ? (accounts.find((account) => account.id === selectedAccountID) ??
      accounts[0] ??
      null)
    : (accounts[0] ?? null);
  const recentQuery = useQuery({
    enabled: selectedAccount !== null,
    queryFn: () =>
      getRecentlyReconciled(recentLimit, selectedAccount?.id ?? null),
    queryKey: queryKeys.banking.recent(
      recentLimit,
      selectedAccount?.id ?? null,
    ),
  });
  const unmatchedQuery = useQuery({
    enabled: selectedAccount !== null,
    queryFn: () =>
      getBankingFeed({
        accountID: selectedAccount?.id ?? null,
        state: "unreconciled",
      }),
    queryKey: queryKeys.banking.feed(
      selectedAccount?.id ?? null,
      "unreconciled",
    ),
  });
  const allReviewCards = useMemo(
    () => flattenReviewQueue(reviewQuery.data),
    [reviewQuery.data],
  );
  const reviewCounts = useMemo(
    () => reviewCountsByAccount(allReviewCards),
    [allReviewCards],
  );
  const scopedReviewCards = useMemo(
    () =>
      selectedAccount
        ? allReviewCards.filter(
            (card) => card.transaction.account_id === selectedAccount.id,
          )
        : [],
    [allReviewCards, selectedAccount],
  );
  const manualAllocationTransactions = useMemo(
    () =>
      selectedAccount
        ? (unmatchedQuery.data?.transactions ?? []).filter(
            (transaction) =>
              transaction.account_id === selectedAccount.id &&
              transaction.amount.amount_minor > 0,
          )
        : [],
    [selectedAccount, unmatchedQuery.data],
  );
  const scopedRecent = selectedAccount
    ? (recentQuery.data?.transactions ?? [])
    : [];

  const importMutation = useMutation({
    mutationFn: ({ accountID, file }: { accountID: number; file: File }) =>
      importBankingCSV(accountID, file),
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSuccess: (summary) => {
      setToast({
        message: `${summary.filename}: ${summary.new} new, ${summary.duplicates} duplicates`,
        tone: "success",
      });
      void refreshBankingData(queryClient);
    },
  });

  const confirmMutation = useMutation<
    BankingCommandResponse,
    Error,
    ConfirmVariables
  >({
    mutationFn: ({ invoiceID, transactionID }) =>
      confirmBankingMatch(transactionID, invoiceID),
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSuccess: (response) => {
      rememberRecentKind(response);
      const fxMessage = hasNonZeroMoney(response.realised_fx_amount)
        ? ` - auto-posted FX ${formatFXResult(response.realised_fx_amount)}`
        : "";
      setToast({
        message: `Confirmed match${fxMessage}`,
        tone: "success",
      });
      void refreshBankingData(queryClient);
    },
  });

  const dlaMutation = useMutation({
    mutationFn: fileBankingTransactionToDLA,
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSuccess: (response) => {
      rememberRecentKind(response);
      const amount = response.amount_gbp
        ? ` ${formatAbsoluteMoney(response.amount_gbp)}`
        : "";
      setToast({
        message: `Filed to DLA${amount}`,
        tone: "success",
      });
      void refreshBankingData(queryClient);
    },
  });

  const recodeMutation = useMutation({
    mutationFn: ({
      accountCode,
      transactionID,
    }: {
      accountCode: string;
      transactionID: number;
    }) => recodeBankingTransaction(transactionID, accountCode),
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSuccess: (response, variables) => {
      rememberRecentKind(response);
      const applied = response.rule
        ? `; rule applied ${formatCount(response.rule.times_applied, "time")}`
        : "";
      setToast({
        message: `Recoded to ${formatAccountCode(variables.accountCode)}${applied}`,
        tone: "success",
      });
      void refreshBankingData(queryClient);
    },
  });

  const excludeMutation = useMutation<
    BankingCommandResponse,
    Error,
    ExcludeVariables,
    ExcludeContext
  >({
    mutationFn: ({ card, reason }) =>
      excludeBankingTransaction(card.transaction.id, reason),
    onError: (error, _variables, context) => {
      if (context?.previousQueue) {
        queryClient.setQueryData(
          queryKeys.banking.review(),
          context.previousQueue,
        );
      }
      setToast({
        message:
          isApiError(error) && error.status === 409
            ? "Exclude conflict; restored the card."
            : problemMessage(error),
        tone: "error",
      });
    },
    onMutate: async ({ card }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.banking.review() });
      const previousQueue = queryClient.getQueryData<BankingReviewQueue>(
        queryKeys.banking.review(),
      );
      if (previousQueue) {
        queryClient.setQueryData(
          queryKeys.banking.review(),
          removeCardFromQueue(previousQueue, card.transaction.id),
        );
      }
      return { previousQueue };
    },
    onSettled: () => {
      void refreshBankingData(queryClient);
    },
    onSuccess: () => {
      setToast({ message: "Transaction excluded.", tone: "success" });
    },
  });

  const attachReceiptMutation = useMutation({
    mutationFn: ({ file, transactionID }: AttachReceiptVariables) =>
      attachBankingReceipt(transactionID, file),
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSettled: () => {
      void refreshBankingData(queryClient);
    },
    onSuccess: (receipt) => {
      setToast({
        message: `Attached ${receipt.filename}`,
        tone: "success",
      });
    },
  });

  const deleteReceiptMutation = useMutation({
    mutationFn: ({ transactionID }: DeleteReceiptVariables) =>
      deleteBankingReceipt(transactionID),
    onError: (error) => {
      setToast({ message: problemMessage(error), tone: "error" });
    },
    onSettled: () => {
      void refreshBankingData(queryClient);
    },
    onSuccess: () => {
      setToast({ message: "Receipt removed.", tone: "success" });
    },
  });

  const isCommandPending =
    confirmMutation.isPending ||
    dlaMutation.isPending ||
    recodeMutation.isPending ||
    excludeMutation.isPending ||
    attachReceiptMutation.isPending ||
    deleteReceiptMutation.isPending;
  const selectedWorkCount = selectedAccount
    ? Math.max(
        scopedReviewCards.length + manualAllocationTransactions.length,
        selectedAccount.unreconciled_count,
      )
    : 0;
  const isEmptyQueue =
    !reviewQuery.isPending &&
    !unmatchedQuery.isPending &&
    selectedAccount !== null &&
    scopedReviewCards.length === 0 &&
    manualAllocationTransactions.length === 0 &&
    selectedAccount.unreconciled_count === 0;
  const hasUnmatchedImports =
    !reviewQuery.isPending &&
    !unmatchedQuery.isPending &&
    selectedAccount !== null &&
    scopedReviewCards.length === 0 &&
    manualAllocationTransactions.length === 0 &&
    selectedAccount.unreconciled_count > 0;
  const queueTitle = selectedAccount
    ? `${selectedAccount.name} review queue`
    : "Review queue";

  const createAccountMutation = useMutation({
    mutationFn: createBankingAccount,
    onError: (error) => {
      setAccountFormError(accountCreateErrorMessage(error));
    },
    onSuccess: (account) => {
      queryClient.setQueryData<BankingAccountsResponse>(
        queryKeys.banking.accounts(),
        (current) => upsertAccountResponse(current, account),
      );
      setSelectedAccountID(account.id);
      setCreateAccountOpen(false);
      setAccountDraft(defaultAccountDraft());
      setAccountFormError(null);
      setToast({
        message: `Created ${account.name}. CSV import ready.`,
        tone: "success",
      });
      void refreshBankingData(queryClient);
    },
  });

  function openCreateAccount() {
    setAccountDraft(defaultAccountDraft());
    setAccountFormError(null);
    setCreateAccountOpen(true);
  }

  function closeCreateAccount() {
    if (createAccountMutation.isPending) {
      return;
    }
    setCreateAccountOpen(false);
    setAccountFormError(null);
  }

  function handleCreateAccount(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const draft = normalizeAccountDraft(accountDraft);
    if (draft.name === "") {
      setAccountFormError("Enter an account name.");
      return;
    }

    const duplicate = accounts.find((account) =>
      accountMatchesDraft(account, draft),
    );
    if (duplicate) {
      setAccountFormError(
        `A ${formatProvider(draft.provider)} ${draft.currency} account named "${draft.name}" already exists.`,
      );
      return;
    }

    setAccountFormError(null);
    createAccountMutation.mutate(draft);
  }

  function handleImportClick() {
    fileInputRef.current?.click();
  }

  function handleImportChange(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file || !selectedAccount) {
      return;
    }
    importMutation.mutate({ accountID: selectedAccount.id, file });
  }

  function rememberRecentKind(response: BankingCommandResponse) {
    const kind = commandKind(response);
    const transactionID = response.transaction?.id;
    if (!kind || !transactionID) {
      return;
    }
    setRecentKinds((current) => ({ ...current, [transactionID]: kind }));
  }

  return (
    <div className="banking-screen">
      <div className="banking-screen__header">
        <div>
          <PageTitle>Banking</PageTitle>
          <p className="type-secondary">
            Import statements, confirm matches, and code owner drawings from one
            review queue.
          </p>
        </div>
        <div className="banking-screen__actions">
          <Link
            className="ui-button ui-button--secondary ui-button--medium"
            to="/banking/payee-rules"
          >
            Payee rules
          </Link>
          <input
            accept=".csv,text/csv"
            aria-label="CSV statement file"
            className="banking-import-input"
            onChange={handleImportChange}
            ref={fileInputRef}
            type="file"
          />
          <Button
            disabled={!selectedAccount || importMutation.isPending}
            onClick={handleImportClick}
            type="button"
          >
            {importMutation.isPending ? "Importing" : "Import CSV"}
          </Button>
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

      {accountsQuery.isError ? (
        <ProblemAlert
          error={accountsQuery.error}
          fallbackTitle="Unable to load bank accounts."
        />
      ) : null}
      {reviewQuery.isError ? (
        <ProblemAlert
          error={reviewQuery.error}
          fallbackTitle="Unable to load review queue."
        />
      ) : null}
      {recentQuery.isError ? (
        <ProblemAlert
          error={recentQuery.error}
          fallbackTitle="Unable to load recently reconciled."
        />
      ) : null}
      {unmatchedQuery.isError ? (
        <ProblemAlert
          error={unmatchedQuery.error}
          fallbackTitle="Unable to load unmatched transactions."
        />
      ) : null}

      <AdvisorStrip surface="banking" />

      <AccountCards
        accounts={accounts}
        isLoading={accountsQuery.isPending}
        onAddAccount={openCreateAccount}
        onSelect={setSelectedAccountID}
        reviewCounts={reviewCounts}
        selectedAccountID={selectedAccount?.id ?? null}
        useReviewCounts={!!reviewQuery.data}
      />

      <SplitMain>
        <section className="banking-review" aria-label={queueTitle}>
          <div className="banking-section-heading">
            <div>
              <p className="type-uppercase-label">Review queue</p>
              <h2>{selectedAccount?.name ?? "No account selected"}</h2>
            </div>
            <Badge variant="count">{selectedWorkCount}</Badge>
          </div>

          {reviewQuery.isPending ? (
            <Card>
              <p className="type-secondary">Loading review queue.</p>
            </Card>
          ) : null}

          {!reviewQuery.isPending && accounts.length === 0 ? (
            <EmptyState title="No bank accounts">
              <span className="banking-empty-state__content">
                <span>
                  Add a Revolut bank account before importing statements.
                </span>
                <span className="banking-empty-state__actions">
                  <Button onClick={openCreateAccount} type="button">
                    Add account
                  </Button>
                </span>
              </span>
            </EmptyState>
          ) : null}

          {scopedReviewCards.map((card) => (
            <ReviewCard
              busy={isCommandPending}
              card={card}
              directors={directors}
              key={`${card.kind}-${card.suggestion_id}`}
              onAttachReceipt={(transactionID, file) =>
                attachReceiptMutation.mutate({ file, transactionID })
              }
              onDeleteReceipt={(transactionID) =>
                deleteReceiptMutation.mutate({ transactionID })
              }
              onExclude={(selectedCard, reason) =>
                excludeMutation.mutate({ card: selectedCard, reason })
              }
              onFileDLA={(transactionID, directorID) =>
                dlaMutation.mutate({ directorID, transactionID })
              }
              onMatchInvoice={(transactionID, invoiceID) =>
                confirmMutation.mutate({ invoiceID, transactionID })
              }
              onRecode={(transactionID, accountCode) =>
                recodeMutation.mutate({ accountCode, transactionID })
              }
            />
          ))}

          {manualAllocationTransactions.map((transaction) => (
            <ManualAllocationCard
              busy={isCommandPending}
              key={`manual-${transaction.id}`}
              onAttachReceipt={(transactionID, file) =>
                attachReceiptMutation.mutate({ file, transactionID })
              }
              onConfirm={(transactionID, invoiceID) =>
                confirmMutation.mutate({ invoiceID, transactionID })
              }
              onDeleteReceipt={(transactionID) =>
                deleteReceiptMutation.mutate({ transactionID })
              }
              transaction={transaction}
            />
          ))}

          {isEmptyQueue ? (
            <EmptyState title="All caught up…">
              No banking review cards are waiting for this account.
            </EmptyState>
          ) : null}

          {hasUnmatchedImports ? (
            <EmptyState title="Review pending">
              Imported transactions are waiting for suggested matches.
            </EmptyState>
          ) : null}
        </section>

        <aside className="banking-rail" aria-label="Banking side panel">
          {isEmptyQueue ? (
            <EmptyState title="All caught up…">
              Recently imported transactions have all been reconciled or
              excluded.
            </EmptyState>
          ) : null}
          <RecentlyReconciled
            busy={isCommandPending}
            isLoading={recentQuery.isPending}
            items={scopedRecent}
            kindByTransactionID={recentKinds}
            onAttachReceipt={(transactionID, file) =>
              attachReceiptMutation.mutate({ file, transactionID })
            }
            onDeleteReceipt={(transactionID) =>
              deleteReceiptMutation.mutate({ transactionID })
            }
          />
        </aside>
      </SplitMain>

      {isCreateAccountOpen ? (
        <AccountCreateModal
          draft={accountDraft}
          error={accountFormError}
          isSubmitting={createAccountMutation.isPending}
          onCancel={closeCreateAccount}
          onChange={setAccountDraft}
          onSubmit={handleCreateAccount}
        />
      ) : null}
    </div>
  );
}

function AccountCards({
  accounts,
  isLoading,
  onAddAccount,
  onSelect,
  reviewCounts,
  selectedAccountID,
  useReviewCounts,
}: {
  readonly accounts: BankingAccount[];
  readonly isLoading: boolean;
  readonly onAddAccount: () => void;
  readonly onSelect: (accountID: number) => void;
  readonly reviewCounts: Map<number, number>;
  readonly selectedAccountID: number | null;
  readonly useReviewCounts: boolean;
}) {
  if (isLoading) {
    return <p className="type-secondary">Loading bank accounts.</p>;
  }

  if (accounts.length === 0) {
    return null;
  }

  return (
    <section className="banking-account-cards" aria-label="Bank accounts">
      {accounts.map((account) => {
        const selected = account.id === selectedAccountID;
        const count = useReviewCounts
          ? Math.max(
              reviewCounts.get(account.id) ?? 0,
              account.unreconciled_count,
            )
          : account.unreconciled_count;
        return (
          <button
            aria-pressed={selected}
            className={
              selected
                ? "banking-account-card banking-account-card--selected"
                : "banking-account-card"
            }
            key={account.id}
            onClick={() => onSelect(account.id)}
            type="button"
          >
            <span className="banking-account-card__meta">
              <span>{formatProvider(account.provider)}</span>
              <strong>{account.name}</strong>
              <span>{account.ledger_account_code}</span>
            </span>
            <span className="banking-account-card__side">
              <span>{account.currency}</span>
              <Badge variant="count">{count}</Badge>
            </span>
          </button>
        );
      })}
      <button
        aria-label="Add account"
        className="banking-account-card banking-account-card--add"
        onClick={onAddAccount}
        type="button"
      >
        <span className="banking-account-card__meta">
          <span>New account</span>
          <strong>Add account</strong>
          <span>Revolut GBP or EUR</span>
        </span>
        <span className="banking-account-card__side" aria-hidden="true">
          <span className="banking-account-card__add-icon">+</span>
        </span>
      </button>
    </section>
  );
}

function AccountCreateModal({
  draft,
  error,
  isSubmitting,
  onCancel,
  onChange,
  onSubmit,
}: {
  readonly draft: BankingCreateAccountRequest;
  readonly error: string | null;
  readonly isSubmitting: boolean;
  readonly onCancel: () => void;
  readonly onChange: (draft: BankingCreateAccountRequest) => void;
  readonly onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <div className="banking-account-modal-backdrop">
      <form
        aria-labelledby="banking-account-modal-title"
        aria-modal="true"
        className="banking-account-modal"
        onSubmit={onSubmit}
        role="dialog"
      >
        <h2 id="banking-account-modal-title">Add bank account</h2>
        <div className="banking-account-modal__fields">
          <Field label="Account name">
            <Input
              autoFocus
              invalid={error !== null && draft.name.trim() === ""}
              onChange={(event) =>
                onChange({ ...draft, name: event.target.value })
              }
              value={draft.name}
            />
          </Field>
          <Field label="Provider">
            <Select
              onChange={(event) =>
                onChange({
                  ...draft,
                  provider: event.target
                    .value as BankingCreateAccountRequest["provider"],
                })
              }
              value={draft.provider}
            >
              <option value="revolut">Revolut</option>
            </Select>
          </Field>
          <Field label="Currency">
            <Select
              onChange={(event) =>
                onChange({
                  ...draft,
                  currency: event.target
                    .value as BankingCreateAccountRequest["currency"],
                })
              }
              value={draft.currency}
            >
              <option value="GBP">GBP</option>
              <option value="EUR">EUR</option>
            </Select>
          </Field>
        </div>
        {error ? (
          <div className="banking-account-modal__error" role="alert">
            {error}
          </div>
        ) : null}
        <div className="banking-account-modal__actions">
          <Button
            disabled={isSubmitting}
            onClick={onCancel}
            type="button"
            variant="secondary"
          >
            Cancel
          </Button>
          <Button disabled={isSubmitting} type="submit">
            {isSubmitting ? "Creating" : "Create account"}
          </Button>
        </div>
      </form>
    </div>
  );
}

function ReviewCard({
  busy,
  card,
  directors,
  onAttachReceipt,
  onDeleteReceipt,
  onExclude,
  onFileDLA,
  onMatchInvoice,
  onRecode,
}: {
  readonly busy: boolean;
  readonly card: BankingReviewCard;
  readonly directors: BankingDirector[];
  readonly onAttachReceipt: (transactionID: number, file: File) => void;
  readonly onDeleteReceipt: (transactionID: number) => void;
  readonly onExclude: (card: BankingReviewCard, reason: string) => void;
  readonly onFileDLA: (transactionID: number, directorID?: string) => void;
  readonly onMatchInvoice: (transactionID: number, invoiceID?: string) => void;
  readonly onRecode: (transactionID: number, accountCode: string) => void;
}) {
  const title = reviewCardTitle(card);
  return (
    <Card
      actions={
        <CardOverflow card={card} disabled={busy} onExclude={onExclude} />
      }
      as="article"
      className={`banking-review-card banking-review-card--${card.kind}`}
      footer={
        <ReviewCardActions
          busy={busy}
          card={card}
          directors={directors}
          onFileDLA={onFileDLA}
          onMatchInvoice={onMatchInvoice}
          onRecode={onRecode}
        />
      }
      title={
        <span className="banking-review-card__title">
          <span className="banking-kind-icon" aria-hidden="true">
            {kindIcon(card.kind)}
          </span>
          <span>{title}</span>
          <Badge variant="neutral">{formatConfidence(card.confidence)}</Badge>
          {card.transaction.receipt ? (
            <Badge variant="neutral">Receipt</Badge>
          ) : null}
        </span>
      }
    >
      <div className="banking-review-card__body">
        <div>
          <p className="banking-review-card__payee">{card.transaction.payee}</p>
          <p className="banking-review-card__reference">
            {card.transaction.reference}
          </p>
        </div>
        <p className="banking-review-card__amount type-mono-numeral">
          {formatMoney(card.transaction.amount)}
        </p>
      </div>

      <ReceiptControls
        busy={busy}
        onAttachReceipt={onAttachReceipt}
        onDeleteReceipt={onDeleteReceipt}
        transaction={card.transaction}
      />

      {card.kind === "match" ? <MatchDetails card={card} /> : null}
      {card.kind === "suggestion" ? <SuggestionDetails card={card} /> : null}
      {card.kind === "rule" ? <RuleDetails card={card} /> : null}
    </Card>
  );
}

function ManualAllocationCard({
  busy,
  onAttachReceipt,
  onConfirm,
  onDeleteReceipt,
  transaction,
}: {
  readonly busy: boolean;
  readonly onAttachReceipt: (transactionID: number, file: File) => void;
  readonly onConfirm: (transactionID: number, invoiceID: string) => void;
  readonly onDeleteReceipt: (transactionID: number) => void;
  readonly transaction: BankingTransaction;
}) {
  return (
    <Card
      as="article"
      className="banking-review-card banking-review-card--manual"
      footer={
        <div className="banking-review-card__actions">
          <ManualInvoicePicker
            busy={busy}
            buttonLabel="Match selected"
            onConfirm={(invoiceID) => onConfirm(transaction.id, invoiceID)}
            transaction={transaction}
          />
        </div>
      }
      title={
        <span className="banking-review-card__title">
          <span className="banking-kind-icon" aria-hidden="true">
            M
          </span>
          <span>Manual match</span>
          <Badge variant="neutral">Unreconciled</Badge>
          {transaction.receipt ? (
            <Badge variant="neutral">Receipt</Badge>
          ) : null}
        </span>
      }
    >
      <div className="banking-review-card__body">
        <div>
          <p className="banking-review-card__payee">{transaction.payee}</p>
          <p className="banking-review-card__reference">
            {transaction.reference}
          </p>
        </div>
        <p className="banking-review-card__amount type-mono-numeral">
          {formatMoney(transaction.amount)}
        </p>
      </div>

      <ReceiptControls
        busy={busy}
        onAttachReceipt={onAttachReceipt}
        onDeleteReceipt={onDeleteReceipt}
        transaction={transaction}
      />
    </Card>
  );
}

function ReceiptControls({
  busy,
  onAttachReceipt,
  onDeleteReceipt,
  transaction,
}: {
  readonly busy: boolean;
  readonly onAttachReceipt: (transactionID: number, file: File) => void;
  readonly onDeleteReceipt: (transactionID: number) => void;
  readonly transaction: BankingTransaction;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const receipt = transaction.receipt;

  function handleFileChange(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) {
      return;
    }
    onAttachReceipt(transaction.id, file);
  }

  return (
    <div className="banking-receipt-controls">
      <input
        accept={receiptAccept}
        aria-label={`Receipt file for ${transaction.payee}`}
        className="banking-import-input"
        onChange={handleFileChange}
        ref={inputRef}
        type="file"
      />
      <Button
        disabled={busy}
        onClick={() => inputRef.current?.click()}
        size="small"
        type="button"
        variant="secondary"
      >
        {receipt ? "Replace receipt" : "Attach receipt"}
      </Button>
      {receipt ? (
        <>
          <a
            className="ui-button ui-button--secondary ui-button--small banking-receipt-link"
            href={receipt.url}
            rel="noreferrer"
            target="_blank"
          >
            Preview receipt
          </a>
          <Button
            disabled={busy}
            onClick={() => onDeleteReceipt(transaction.id)}
            size="small"
            type="button"
            variant="danger"
          >
            Delete receipt
          </Button>
        </>
      ) : null}
    </div>
  );
}

function MatchDetails({ card }: { readonly card: BankingReviewCard }) {
  const invoiceLabel = isDraftInvoiceMatch(card)
    ? "Draft invoice"
    : (card.target.invoice_number ?? card.target.id);
  return (
    <div className="banking-review-card__detail">
      <p>
        <strong>{invoiceLabel}</strong>
        {card.target.client ? <span> · {card.target.client}</span> : null}
      </p>
      <p>{card.explanation}</p>
    </div>
  );
}

function SuggestionDetails({ card }: { readonly card: BankingReviewCard }) {
  return (
    <div className="banking-review-card__detail">
      <p>
        <strong>DLA drawing</strong>
        {card.target.id ? <span> · {card.target.id}</span> : null}
      </p>
      <p>{card.explanation}</p>
    </div>
  );
}

function RuleDetails({ card }: { readonly card: BankingReviewCard }) {
  const accountCode = card.target.account_code ?? "";
  return (
    <div className="banking-review-card__detail">
      <p>
        <strong>{card.transaction.payee}</strong>
        <span> → {formatAccountCode(accountCode)}</span>
      </p>
      <p>
        Applied {formatCount(card.target.times_applied ?? 0, "time")} by payee
        rule.
      </p>
      <p>{card.explanation}</p>
    </div>
  );
}

function ReviewCardActions({
  busy,
  card,
  directors,
  onFileDLA,
  onMatchInvoice,
  onRecode,
}: {
  readonly busy: boolean;
  readonly card: BankingReviewCard;
  readonly directors: BankingDirector[];
  readonly onFileDLA: (transactionID: number, directorID?: string) => void;
  readonly onMatchInvoice: (transactionID: number, invoiceID?: string) => void;
  readonly onRecode: (transactionID: number, accountCode: string) => void;
}) {
  const needsDirector =
    card.target.id === "director-loan" && directors.length > 1;
  const [selectedDirectorID, setSelectedDirectorID] = useState(
    directors[0]?.id ?? "director-1",
  );
  const manualInvoicePicker = canManuallyAllocateSuggestedTransaction(card) ? (
    <ManualInvoicePicker
      busy={busy}
      buttonLabel="Match selected"
      onConfirm={(invoiceID) => onMatchInvoice(card.transaction.id, invoiceID)}
      transaction={card.transaction}
    />
  ) : null;

  if (card.kind === "match") {
    return (
      <div className="banking-review-card__actions">
        <Button
          disabled={busy}
          onClick={() => onMatchInvoice(card.transaction.id)}
          size="small"
          type="button"
        >
          {isDraftInvoiceMatch(card) ? "Send + allocate" : "Confirm"}
        </Button>
        <ManualInvoicePicker
          busy={busy}
          buttonLabel="Confirm selected"
          defaultInvoiceID={card.target.id}
          onConfirm={(invoiceID) =>
            onMatchInvoice(card.transaction.id, invoiceID)
          }
          transaction={card.transaction}
        />
      </div>
    );
  }

  if (card.kind === "suggestion") {
    return (
      <div className="banking-review-card__actions">
        {needsDirector ? (
          <Select
            aria-label="DLA director"
            onChange={(event) => setSelectedDirectorID(event.target.value)}
            value={selectedDirectorID}
          >
            {directors.map((director) => (
              <option key={director.id} value={director.id}>
                {director.name}
              </option>
            ))}
          </Select>
        ) : null}
        <Button
          disabled={busy}
          onClick={() =>
            onFileDLA(
              card.transaction.id,
              needsDirector ? selectedDirectorID : undefined,
            )
          }
          size="small"
          type="button"
        >
          File to DLA
        </Button>
        <RecodePicker
          busy={busy}
          label="DLA recode"
          onRecode={(accountCode) => onRecode(card.transaction.id, accountCode)}
        />
        {manualInvoicePicker}
      </div>
    );
  }

  return (
    <div className="banking-review-card__actions">
      <Button
        disabled={busy || !card.target.account_code}
        onClick={() => {
          if (card.target.account_code) {
            onRecode(card.transaction.id, card.target.account_code);
          }
        }}
        size="small"
        type="button"
      >
        Apply
      </Button>
      <RecodePicker
        busy={busy}
        defaultAccountCode={card.target.account_code}
        label="Rule recode"
        onRecode={(accountCode) => onRecode(card.transaction.id, accountCode)}
      />
      {manualInvoicePicker}
    </div>
  );
}

function ManualInvoicePicker({
  busy,
  buttonLabel,
  defaultInvoiceID = "",
  onConfirm,
  transaction,
}: {
  readonly busy: boolean;
  readonly buttonLabel: string;
  readonly defaultInvoiceID?: string;
  readonly onConfirm: (invoiceID: string) => void;
  readonly transaction: BankingTransaction;
}) {
  const [invoiceID, setInvoiceID] = useState(defaultInvoiceID);
  const candidatesQuery = useQuery({
    queryFn: () => getBankingInvoiceCandidates(transaction.id),
    queryKey: queryKeys.banking.candidates(transaction.id),
  });
  const candidates = candidatesQuery.data?.candidates ?? [];

  return (
    <details className="banking-invoice-picker">
      <summary>Match to invoice ▾</summary>
      <div className="banking-invoice-picker__panel">
        {candidatesQuery.isError ? (
          <p className="type-secondary">
            {problemMessage(candidatesQuery.error)}
          </p>
        ) : null}
        <label>
          <span>Invoice</span>
          <select
            disabled={busy || candidatesQuery.isPending}
            onChange={(event) => setInvoiceID(event.target.value)}
            value={invoiceID}
          >
            <option value="">
              {candidatesQuery.isPending
                ? "Loading invoices"
                : "Select invoice"}
            </option>
            {candidates.map((candidate) => (
              <option key={candidate.invoice_id} value={candidate.invoice_id}>
                {invoiceCandidateLabel(candidate)}
              </option>
            ))}
          </select>
        </label>
        {!candidatesQuery.isPending && candidates.length === 0 ? (
          <p className="type-secondary">No open invoices.</p>
        ) : null}
        <Button
          disabled={busy || candidatesQuery.isPending || invoiceID === ""}
          onClick={() => onConfirm(invoiceID)}
          size="small"
          type="button"
          variant="secondary"
        >
          {buttonLabel}
        </Button>
      </div>
    </details>
  );
}

function RecodePicker({
  busy,
  defaultAccountCode = defaultExpenseAccountCode,
  label,
  onRecode,
}: {
  readonly busy: boolean;
  readonly defaultAccountCode?: string;
  readonly label: string;
  readonly onRecode: (accountCode: string) => void;
}) {
  const [accountCode, setAccountCode] = useState(defaultAccountCode);
  return (
    <details className="banking-recode">
      <summary>Recode ▾</summary>
      <div className="banking-recode__panel">
        <ExpenseCategoryPicker
          disabled={busy}
          label={`${label} account`}
          onChange={setAccountCode}
          value={accountCode}
        />
        <Button
          disabled={busy || accountCode === ""}
          onClick={() => onRecode(accountCode)}
          size="small"
          type="button"
          variant="secondary"
        >
          Recode selected
        </Button>
      </div>
    </details>
  );
}

function CardOverflow({
  card,
  disabled,
  onExclude,
}: {
  readonly card: BankingReviewCard;
  readonly disabled: boolean;
  readonly onExclude: (card: BankingReviewCard, reason: string) => void;
}) {
  function handleExclude() {
    const reason = window.prompt("Reason for excluding this transaction?");
    const trimmed = reason?.trim();
    if (trimmed) {
      onExclude(card, trimmed);
    }
  }

  return (
    <details className="banking-card-menu">
      <summary aria-label={`Options for ${card.transaction.payee}`}>
        ...
      </summary>
      <div className="banking-card-menu__panel" role="menu">
        <button
          disabled={disabled}
          onClick={handleExclude}
          role="menuitem"
          type="button"
        >
          Exclude
        </button>
      </div>
    </details>
  );
}

function RecentlyReconciled({
  busy,
  isLoading,
  items,
  kindByTransactionID,
  onAttachReceipt,
  onDeleteReceipt,
}: {
  readonly busy: boolean;
  readonly isLoading: boolean;
  readonly items: BankingRecentTransaction[];
  readonly kindByTransactionID: RecentKindByID;
  readonly onAttachReceipt: (transactionID: number, file: File) => void;
  readonly onDeleteReceipt: (transactionID: number) => void;
}) {
  return (
    <Card title="Recently reconciled">
      {isLoading ? (
        <p className="type-secondary">Loading recent reconciliations.</p>
      ) : null}
      {!isLoading && items.length === 0 ? (
        <p className="type-secondary">No reconciliations yet.</p>
      ) : null}
      {items.length > 0 ? (
        <ul className="banking-recent-list">
          {items.map((item) => (
            <li key={`${item.transaction.id}-${item.reconciled_at}`}>
              <span
                aria-label={recentKindLabel(item, kindByTransactionID)}
                className="banking-kind-icon"
                role="img"
              >
                {recentKindIcon(item, kindByTransactionID)}
              </span>
              <span className="banking-recent-list__copy">
                <span>{formatShortDate(item.transaction.date)}</span>
                <strong>{item.transaction.payee}</strong>
              </span>
              <span className="type-mono-numeral">
                {formatMoney(item.transaction.amount)}
              </span>
              <span className="banking-recent-list__receipt">
                {item.transaction.receipt ? (
                  <Badge variant="neutral">Receipt</Badge>
                ) : null}
                <ReceiptControls
                  busy={busy}
                  onAttachReceipt={onAttachReceipt}
                  onDeleteReceipt={onDeleteReceipt}
                  transaction={item.transaction}
                />
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </Card>
  );
}

function normalizeAccountDraft(
  draft: BankingCreateAccountRequest,
): BankingCreateAccountRequest {
  return {
    currency: draft.currency
      .trim()
      .toUpperCase() as BankingCreateAccountRequest["currency"],
    name: draft.name.trim(),
    provider: draft.provider
      .trim()
      .toLowerCase() as BankingCreateAccountRequest["provider"],
  };
}

function accountMatchesDraft(
  account: BankingAccount,
  draft: BankingCreateAccountRequest,
) {
  return (
    account.provider === draft.provider &&
    account.currency === draft.currency &&
    account.name === draft.name
  );
}

function upsertAccountResponse(
  current: BankingAccountsResponse | undefined,
  account: BankingAccount,
): BankingAccountsResponse {
  if (!current) {
    return { accounts: [account] };
  }
  const accounts = current.accounts.some((item) => item.id === account.id)
    ? current.accounts.map((item) => (item.id === account.id ? account : item))
    : [...current.accounts, account];
  return { accounts };
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

async function refreshBankingData(
  queryClient: ReturnType<typeof useQueryClient>,
) {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: queryKeys.banking.accounts() }),
    queryClient.invalidateQueries({ queryKey: queryKeys.banking.review() }),
    queryClient.invalidateQueries({ queryKey: ["banking", "feed"] }),
    queryClient.invalidateQueries({
      queryKey: ["banking", "invoiceCandidates"],
    }),
    queryClient.invalidateQueries({ queryKey: ["banking", "recent"] }),
  ]);
}

function flattenReviewQueue(queue: BankingReviewQueue | undefined) {
  return [
    ...(queue?.matches ?? []),
    ...(queue?.suggestions ?? []),
    ...(queue?.rules ?? []),
  ];
}

function reviewCountsByAccount(cards: BankingReviewCard[]) {
  const counts = new Map<number, number>();
  for (const card of cards) {
    counts.set(
      card.transaction.account_id,
      (counts.get(card.transaction.account_id) ?? 0) + 1,
    );
  }
  return counts;
}

function removeCardFromQueue(
  queue: BankingReviewQueue,
  transactionID: number,
): BankingReviewQueue {
  return {
    matches: queue.matches.filter(
      (card) => card.transaction.id !== transactionID,
    ),
    rules: queue.rules.filter((card) => card.transaction.id !== transactionID),
    suggestions: queue.suggestions.filter(
      (card) => card.transaction.id !== transactionID,
    ),
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

function accountCreateErrorMessage(error: unknown) {
  if (isApiError(error)) {
    const detail = error.problem.detail ?? "";
    const title = error.problem.title ?? "";
    const text = `${title} ${detail}`.toLowerCase();
    if (
      error.status === 409 ||
      text.includes("duplicate") ||
      text.includes("already exists")
    ) {
      return "That bank account already exists. Choose a different name or currency.";
    }
    if (error.status === 422 && text.includes("account name")) {
      return "Enter an account name.";
    }
  }
  return problemMessage(error);
}

function reviewCardTitle(card: BankingReviewCard) {
  switch (card.kind) {
    case "match":
      return isDraftInvoiceMatch(card)
        ? "Draft invoice match"
        : "Invoice match";
    case "rule":
      return "Payee rule";
    case "suggestion":
      return "DLA suggestion";
  }
}

function isDraftInvoiceMatch(card: BankingReviewCard) {
  return card.kind === "match" && card.target.invoice_status === "draft";
}

function kindIcon(kind: BankingReviewCard["kind"]) {
  switch (kind) {
    case "match":
      return "M";
    case "rule":
      return "R";
    case "suggestion":
      return "D";
  }
}

function recentKindIcon(
  item: BankingRecentTransaction,
  kindByTransactionID: RecentKindByID,
) {
  const label = recentKindLabel(item, kindByTransactionID);
  if (label === "DLA") {
    return "D";
  }
  if (label === "Rule") {
    return "R";
  }
  return "M";
}

function recentKindLabel(
  item: BankingRecentTransaction,
  kindByTransactionID: RecentKindByID,
) {
  const rememberedKind = kindByTransactionID[item.transaction.id];
  if (rememberedKind === "suggestion") {
    return "DLA";
  }
  if (rememberedKind === "rule") {
    return "Rule";
  }
  if (rememberedKind === "match") {
    return "Match";
  }
  const reference = item.transaction.reference.toLowerCase();
  const payee = item.transaction.payee.toLowerCase();
  if (payee.includes("meyer") || reference.includes("director")) {
    return "DLA";
  }
  if (reference.includes("subscription") || reference.includes("software")) {
    return "Rule";
  }
  return "Match";
}

function profileDirectors(
  directors: readonly { id?: string; name?: string }[] | undefined,
): BankingDirector[] {
  const mapped =
    directors
      ?.map((director, index) => ({
        id: director.id?.trim() || `director-${index + 1}`,
        name: director.name?.trim() || `Director ${index + 1}`,
      }))
      .filter((director) => director.name !== "") ?? [];
  return mapped.length > 0 ? mapped : [{ id: "director-1", name: "Director" }];
}

function commandKind(response: BankingCommandResponse) {
  switch (response.kind) {
    case "match":
    case "rule":
    case "suggestion":
      return response.kind;
    default:
      return null;
  }
}

function formatProvider(provider: BankingAccount["provider"]) {
  switch (provider) {
    case "revolut":
      return "Revolut Business";
  }
}

function formatCount(count: number, noun: string) {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

function formatMoney(value: BankingMoney) {
  return formatMinorUnits({
    amountMinor: value.amount_minor,
    currency: value.currency,
  });
}

function invoiceCandidateLabel(candidate: BankingInvoiceCandidate) {
  const number = candidate.invoice_number || candidate.invoice_id;
  return `${number} - ${candidate.client} - ${formatMoney(candidate.amount)} - due ${formatShortDate(candidate.due_date)}`;
}

function formatAbsoluteMoney(value: BankingMoney) {
  return formatMinorUnits({
    amountMinor: Math.abs(value.amount_minor),
    currency: value.currency,
  });
}

function hasNonZeroMoney(
  value: BankingMoney | undefined,
): value is BankingMoney {
  return value !== undefined && value.amount_minor !== 0;
}

function canManuallyAllocateSuggestedTransaction(card: BankingReviewCard) {
  return card.kind !== "match" && card.transaction.amount.amount_minor > 0;
}

function formatFXResult(value: BankingMoney) {
  const kind = value.amount_minor < 0 ? "loss" : "gain";
  return `${kind} ${formatAbsoluteMoney(value)}`;
}

function formatShortDate(value: string) {
  return new Intl.DateTimeFormat("en-GB", {
    day: "2-digit",
    month: "short",
    timeZone: "UTC",
  }).format(new Date(`${value}T00:00:00Z`));
}
