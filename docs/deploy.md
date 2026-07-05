# Deploy Ledgerly

Ledgerly deploys as one Docker image: the `ledgerly` Go binary plus headless Chromium for chromedp PDF rendering.

## Local Production Compose

Create an environment file:

```sh
cp .env.prod.example .env.prod
export GIT_SHA="$(git rev-parse --short HEAD)"
```

The compose file loads `.env.prod.example` first, then optional `.env.prod`, for
container runtime environment. Values in `.env.prod` override the defaults even
when Compose is run without `--env-file`. Keep `GIT_SHA` and any host port
override exported, or pass them through `--env-file`, because the image tag,
build argument, and host port mapping are Compose interpolation values.

Start the stack:

```sh
docker compose -f docker-compose.prod.yml up --build -d
```

Check the stamped version:

```sh
curl -fsS http://localhost:8080/healthz
```

The JSON response includes `"version":"<git-sha>"`.

Run migrations as a one-off command:

```sh
docker compose -f docker-compose.prod.yml run --rm app migrate
```

The production image includes `db/migrations` and defaults `LEDGERLY_MIGRATIONS_DIR` to that packaged path, so the one-off command applies the bootstrap schema migrations inside the container.

## Environment Variables

| Variable | Required | Default | Purpose |
|---|---:|---|---|
| `GIT_SHA` | yes | `dev` | Build-stamped version passed to `-ldflags -X main.version=$GIT_SHA`. |
| `POSTGRES_USER` | yes | `ledgerly` | PostgreSQL role created by the `db` service. |
| `POSTGRES_PASSWORD` | yes | `ledgerly` | PostgreSQL password for the app role. Replace for any shared environment. |
| `POSTGRES_DB` | yes | `ledgerly` | PostgreSQL database name. |
| `LEDGERLY_DATABASE_URL` | yes | `postgres://ledgerly:ledgerly@db:5432/ledgerly?sslmode=disable` | App database connection string. |
| `LEDGERLY_MIGRATIONS_DIR` | no | `/usr/local/share/ledgerly/db/migrations` | Container path to packaged SQL migrations for one-off `ledgerly migrate` runs. |
| `LEDGERLY_ENV` | yes | `prod` | Runtime environment; valid values are `dev` and `prod`. |
| `LEDGERLY_LOG_LEVEL` | yes | `info` | Structured log level: `debug`, `info`, `warn`, or `error`. |
| `LEDGERLY_HTTP_ADDR` | no | `:8080` | Address the app listens on inside the container. |
| `LEDGERLY_HTTP_PORT` | no | `8080` | Host port mapped to container port `8080`. |

## Chromium Smoke Test

The image includes a chromedp smoke command that renders `about:blank` to PDF through the bundled Chromium binary:

```sh
docker compose -f docker-compose.prod.yml run --rm app chrome-smoke /tmp/about-blank.pdf
```

Expected output:

```text
rendered about:blank PDF to /tmp/about-blank.pdf
```

## Backups

HLD section 5 calls for nightly `pg_dump` backups. For a compose deployment, schedule this from the host:

```sh
mkdir -p backups
docker compose -f docker-compose.prod.yml exec -T db \
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > "backups/ledgerly-$(date +%F).dump"
```

Keep backups off-host or in a provider-managed backup bucket. Periodically verify restore by loading the dump into a fresh PostgreSQL container.

## Image Size

HLD section 6 expects a larger deploy image because Chromium ships with the app. The target trade-off is roughly 300 MB.

CV-251 validation measured `ledgerly:cv-251` at `149582058` bytes, which is about 143 MiB / 150 MB decimal. Record the current deploy image size after each validation with:

```sh
docker image inspect "ledgerly:${GIT_SHA}" --format '{{.Size}}'
```
