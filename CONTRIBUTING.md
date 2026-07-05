# Contributing to Ledgerly

Thanks for your interest in contributing! Ledgerly is a young project with a fully specified design — most of the value right now is in implementing the [module designs](docs/design/) faithfully. This guide covers the dev environment, the architectural rules PRs are held to, and the workflow.

## Development environment

The toolchain is pinned with [mise](https://mise.jdx.dev) — install it once, then:

```sh
mise install     # installs Go, Node, Python, Task, golangci-lint, and uv at pinned versions
task setup       # one-time setup (Python env sync, etc.)
task --list      # everything that's automated
```

PostgreSQL runs as a service, not a host install: `docker compose up -d` brings up Postgres 16 via [Docker Compose](docker-compose.yml) (Docker required).

mise also auto-creates and activates a Python virtualenv at `.venv/` when you `cd` into the repo (used by agent tooling under `.agents/`). Python dependencies are managed with [uv](https://docs.astral.sh/uv/) via `pyproject.toml`.

Common tasks:

| Command | What it does |
|---|---|
| `task setup` | One-time project setup |
| `docker compose up -d` / `docker compose stop` | Run PostgreSQL 16 in Docker (creates `ledgerly_dev`) |
| `docker compose exec db psql -U postgres -d ledgerly_dev` | Open a psql shell into the dev database |
| `docker compose down -v` | Destroy the DB container and its data volume |
| `task lint` / `task fmt` | Lint / format the Go backend |
| `task test` | Run backend tests |
| `task build` | Build the `ledgerly` binary |
| `task dev` | Run the app in development mode |

Backend/frontend tasks check for `go.mod` / `web/package.json` and tell you plainly if that part of the codebase isn't scaffolded yet.

## Repository layout

```
ledgerly/
├── cmd/ledgerly/             # main: wiring, DI, HTTP server, migrations, cron
├── internal/
│   ├── platform/             # db pool, event bus, http middleware, config, clock
│   └── <module>/             # ledger, moneyfx, invoicing, banking, dla, dividends,
│                             # reports, jurisdiction, advisor, identity
├── web/                      # React SPA (Vite, TS)
├── packs/<jurisdiction>/<v>/ # versioned rules pack data (go:embed)
├── db/migrations/<module>/   # per-module SQL migrations
└── docs/design/              # HLD + module design docs
```

Each module directory has the same shape: `api.go` (public interface), `events.go` (published events), `service.go`, `store.go` (private SQL), `http.go` (REST handlers).

## The rules reviews enforce

These come from the [high-level design](docs/design/hld.md) and are non-negotiable; CI will grow checks for each of them:

1. **Module boundaries.** `internal/<module>/...` may import only another module's root package (its `api.go` surface) and `internal/platform`. Never reach into another module's internals.
2. **No cross-schema SQL.** Each module owns its PostgreSQL schema. Cross-module references are by opaque ID + module name (`source_module`, `source_ref`), never foreign keys across schemas.
3. **Only the ledger writes ledger rows.** Every module records financial facts through core/ledger's API. The journal is append-only — no UPDATE or DELETE on ledger tables, ever.
4. **No floats in money paths.** Money is `{Amount int64, Currency string}`. All arithmetic and allocation goes through the moneyfx package (largest-remainder allocation).
5. **No hard-coded jurisdiction data.** Tax rates, deadlines, thresholds, and advisor wording live in the rules pack under `packs/`. If you're typing `0.20` or a filing deadline into a feature module, stop.

## Workflow

1. **Open or claim an issue** describing the change. Implementation work is tracked against the design docs' work-item breakdowns.
2. **Branch** from `main`, keep changes scoped to one concern.
3. **Before pushing:** `task fmt && task lint && task test`.
4. **Open a PR** using the template. Say which module(s) you touched and confirm the boundary checklist.
5. **Review.** Expect firm feedback on the rules above — this is bookkeeping software; correctness is the product.

> **Note on agent-authored PRs:** some PRs are opened by autonomous agents orchestrated via [Symphony](WORKFLOW.md) against the Linear backlog. They follow the same workflow and review bar as human PRs — the `symphony` label tells you which is which.

### Commit messages

Short imperative subject line ("Add rate-lock table migration"), body explaining *why* when it isn't obvious. Reference issues where relevant.

### Design changes

The design docs under `docs/design/` are the source of truth. If your change alters behaviour described there, update the doc in the same PR — code and design must not drift apart.

## Questions?

Open a GitHub issue, or reach the maintainer ([@npmulder](https://github.com/npmulder)) on GitHub.
