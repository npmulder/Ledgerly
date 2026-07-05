export function cx(
  ...classNames: ReadonlyArray<string | false | null | undefined>
) {
  return classNames.filter(Boolean).join(" ");
}
