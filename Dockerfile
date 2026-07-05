FROM golang:1.24-bookworm AS go-toolchain

FROM node:22-bookworm-slim AS web-build

WORKDIR /src/web

COPY web/.npmrc web/package.json web/package-lock.json ./
RUN npm ci

COPY web ./
RUN npm run build && mkdir -p dist && touch dist/.ledgerly-embed

FROM go-toolchain AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web-build /src/web/dist ./web/dist

ARG GIT_SHA=dev
ARG TARGETOS=linux
ARG TARGETARCH

RUN set -eux; \
	GOOS="${TARGETOS:-linux}"; \
	export GOOS CGO_ENABLED=0; \
	if [ -n "${TARGETARCH:-}" ]; then export GOARCH="${TARGETARCH}"; fi; \
	go build -trimpath -ldflags="-s -w -X main.version=${GIT_SHA}" -o /out/ledgerly ./cmd/ledgerly

FROM chromedp/headless-shell:latest AS golden-test

USER root

RUN apt-get update \
	&& apt-get install -y --no-install-recommends gcc libc6-dev \
	&& rm -rf /var/lib/apt/lists/*

COPY --from=go-toolchain /usr/local/go /usr/local/go
COPY --from=go-toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV CHROME_BIN=/headless-shell/headless-shell \
	CGO_ENABLED=1 \
	HOME=/tmp \
	XDG_CACHE_HOME=/tmp/.cache \
	XDG_CONFIG_HOME=/tmp/.config \
	GOCACHE=/tmp/go-build \
	GOMODCACHE=/tmp/go/pkg/mod \
	PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

WORKDIR /src

ENTRYPOINT []

FROM chromedp/headless-shell:latest AS runtime

ENV CHROME_BIN=/headless-shell/headless-shell \
	LEDGERLY_MIGRATIONS_DIR=/usr/local/share/ledgerly/db/migrations \
	LEDGERLY_HTTP_ADDR=:8080 \
	HOME=/tmp \
	XDG_CACHE_HOME=/tmp/.cache \
	XDG_CONFIG_HOME=/tmp/.config

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ledgerly /usr/local/bin/ledgerly
COPY --from=build /src/db/migrations /usr/local/share/ledgerly/db/migrations

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/ledgerly"]
CMD ["serve"]
