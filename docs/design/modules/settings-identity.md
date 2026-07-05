# Module: settings/identity

**Package:** `internal/identity` · **Schema:** `identity` · **Depends on:** nothing (leaf; everything reads it)

## Responsibility

Company profile (name, logo, company number, registered office, incorporation date, year end), users/auth. Screen: 09 Settings (also hosts clients — owned by invoicing — and jurisdiction pack card — owned by jurisdiction; settings is a shell with per-module sections).

## Domain model

- **CompanyProfile** — `{tradingName, legalName, companyNumber, registeredOffice, incorporationDate, yearEnd, logoAssetID, shareholders[{name, shares, class}], bankDetails (SEPA footer), vatNumber}`. Sample: NPM Limited, 137792C, 18 Athol St Douglas, year end 31 Mar, N. Meyer 100 ordinary £1.
- **User** — `{email, passwordHash, name}`. Session auth (secure cookie). Single-tenant; keep boring.
- **Logo asset** — uploaded image stored on disk/object path, content-addressed; dashed drop area + "Replace logo…" per handoff.

## Propagation requirement (handoff-critical)

Editable trading name + replaceable logo must propagate **everywhere**: app header, invoice PDF, dividend voucher, board minutes. Mechanism: consumers always read via `Identity.Profile()` at render time — **no copies** of name/logo anywhere. Already-rendered stored documents intentionally keep the identity they were generated with (immutability of issued documents); only new renders pick up changes. Publish `identity.ProfileUpdated` so open UI refreshes.

## Public API (Go)

```go
type Identity interface {
    Profile() (CompanyProfile, error)         // the most-called read in the app
    UpdateProfile(patch) error
    ReplaceLogo(upload) (AssetID, error)
    CompanyFacts() (Facts, error)             // incorporation date, year end — for jurisdiction/reports
    // auth: Login, Logout, CurrentUser
}
```

## Settings screen (09) composition

Left nav: Company (this module) / Jurisdiction (pack card from jurisdiction) / Clients (from invoicing) / Invoicing defaults (invoicing) / Bank connections (banking) / Users (this module). Frontend composes; each section calls its owning module's API — settings owns only the shell.

## Events

Publishes: `identity.ProfileUpdated`. Consumes: none.

## Data (schema `identity`)

`company_profile` (single row), `users`, `sessions`, `assets`.

## Work items

1. Profile model + single-row semantics + endpoints
2. Logo upload/replace + asset storage
3. Auth (register-on-first-run, login, sessions, middleware)
4. Settings shell screen + Company section
5. Propagation verification test: change name/logo → header + fresh PDF renders reflect it; stored docs don't
