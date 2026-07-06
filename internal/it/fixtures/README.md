# Integration Fixtures

Scenario fixtures keep integration tests focused on behavior, not setup.

Use the terse helpers for canonical setup:

```go
h := harness.New(t, harness.Options{ClockStart: fixtures.TaxYear2526.Start})
fixtures.Company(t, h)
fixtures.Rates(t, h)
```

`Company` seeds NPM Limited through the identity HTTP API. It writes the
canonical profile from the design handoff: company number `137792C`, registered
office `18 Athol St, Douglas, IM`, 31 March year end, N. Meyer as sole
shareholder, and Revolut Business SEPA footer details.

Use overrides for edge cases:

```go
company := fixtures.Company(t, h).With(
	fixtures.CompanyYearEnd(time.December, 31),
	fixtures.CompanyIncorporationDate(time.Date(2021, time.January, 4, 0, 0, 0, 0, time.UTC)),
)
```

`Rates` seeds ECB EUR-base rows through `moneyfx.Store`. With no preset it uses
`RatesFlat085`, a constant EUR to GBP `0.8500` table covering `TaxYear2526`
(`2025-04-06` through `2026-04-05`). Use `RatesStep(map[time.Time]string)` for
realised-FX scenarios where rates vary by date.

`TryCompany` and `TryRates` return `ErrAlreadySeeded` when a fixture is seeded
twice into the same harness. The plain helpers fail the test immediately.

`Rand(t)` returns a deterministic `math/rand.Rand` seeded from `t.Name()` and
logs the seed for reproduction.

## Extension Contract

Future builders belong in this package when they make integration suites read
like user scenarios and when they can seed through public module APIs.

- CV-228 owns client builders for Contoso GmbH (EUR retainer) and Fabrikam Ltd
  (GBP day-rate). CV-281 does not add or expand them; builders must go through
  invoicing public APIs.
- CV-230 adds a bank CSV generator for Revolut GBP/EUR statement files with
  configurable payees, amounts, and duplicates. Builders must go through public
  import/API paths.

Each module epic's screen or API sub-issue should reference the builder it adds
here. Do not add golden files in this package.
