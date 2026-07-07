export const recodeAccounts = [
  { label: "Fees", value: "5000-fees" },
  { label: "Software", value: "5010-software" },
  { label: "Travel", value: "5020-travel" },
  { label: "Office", value: "5030-office" },
] as const;

export function formatAccountCode(accountCode: string) {
  return (
    recodeAccounts.find((account) => account.value === accountCode)?.label ??
    accountCode
  );
}
