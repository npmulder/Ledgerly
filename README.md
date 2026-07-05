# Ledgerly

Jurisdiction-aware bookkeeping for single-director companies.

The repository layout follows [docs/design/hld.md](docs/design/hld.md), especially the repository layout in section 2.

## Tasks

Install go-task with:

```sh
go install github.com/go-task/task/v3/cmd/task@latest
```

Current `task --list` output:

```text
task: Available tasks for this project:
* build:       Build the ledgerly binary
* lint:        Run Go formatting and vet checks
* run:         Run the ledgerly skeleton
* test:        Run Go tests
```

## Module Directory Convention

Each module under `internal/<module>/` will use:

- `api.go` for the public interface other modules may import
- `events.go` for published event types
- `service.go` for module orchestration
- `store.go` for private persistence
- `http.go` for REST handlers
