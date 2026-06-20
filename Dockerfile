FROM golang:1.25.11-bookworm AS build
WORKDIR /src
ARG ABRA_VERSION=dev
ARG ABRA_COMMIT=unknown
ARG ABRA_DATE=unknown
ARG TARGETOS=linux
ARG TARGETARCH
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN set -eux; \
    goarch="${TARGETARCH:-$(go env GOARCH)}"; \
    runtime_version="${ABRA_VERSION#v}"; \
    runtime_ldflags="-s -w -X github.com/hermawan22/abra/internal/version.Version=${runtime_version} -X github.com/hermawan22/abra/internal/version.Commit=${ABRA_COMMIT} -X github.com/hermawan22/abra/internal/version.Date=${ABRA_DATE}"; \
    cli_ldflags="${runtime_ldflags} -X main.version=${ABRA_VERSION} -X main.commit=${ABRA_COMMIT} -X main.date=${ABRA_DATE}"; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${goarch}" go build -trimpath -ldflags="${runtime_ldflags}" -o /out/abra-api ./cmd/abra-api; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${goarch}" go build -trimpath -ldflags="${runtime_ldflags}" -o /out/abra-worker ./cmd/abra-worker; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${goarch}" go build -trimpath -ldflags="${runtime_ldflags}" -o /out/abra-migrate ./cmd/abra-migrate; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${goarch}" go build -trimpath -ldflags="${cli_ldflags}" -o /out/abra ./cmd/abra

FROM debian:bookworm-slim AS runtime
WORKDIR /app
ENV NODE_ENV=production
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git openssh-client \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd --gid 10001 abra \
    && useradd --uid 10001 --gid 10001 --home-dir /app --shell /usr/sbin/nologin --no-create-home abra
COPY --from=build /out/abra-api /app/abra-api
COPY --from=build /out/abra-worker /app/abra-worker
COPY --from=build /out/abra-migrate /app/abra-migrate
COPY --from=build /out/abra /app/abra
COPY migrations ./migrations
USER abra
EXPOSE 8080
CMD ["/app/abra-api"]
