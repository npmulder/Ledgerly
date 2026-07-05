## Summary

<!-- What does this PR do, and why? Link the issue it addresses. -->

## Module(s) touched

<!-- e.g. invoicing, core/money-fx, web, docs, tooling -->

## Boundary checklist

<!-- These are the rules from CONTRIBUTING.md / the HLD. Check what applies; delete lines that don't. -->

- [ ] Modules import only other modules' `api.go` surface and `internal/platform`
- [ ] No SQL touches another module's schema
- [ ] Financial facts are posted through core/ledger's API only; no UPDATE/DELETE on ledger tables
- [ ] No floats in money paths; arithmetic/allocation via moneyfx
- [ ] No tax rates, deadlines, or advisor wording hard-coded outside the jurisdiction pack
- [ ] Design docs under `docs/design/` updated if behaviour described there changed

## Testing

<!-- How was this verified? `task test` output, manual steps, screenshots for UI changes. -->
