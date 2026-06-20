FROM golang:1.25.11-bookworm AS build
WORKDIR /src
ARG ABRA_VERSION=dev
ARG ABRA_COMMIT=unknown
ARG ABRA_DATE=unknown
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/abra-api ./cmd/abra-api
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/abra-worker ./cmd/abra-worker
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/abra-migrate ./cmd/abra-migrate
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${ABRA_VERSION} -X main.commit=${ABRA_COMMIT} -X main.date=${ABRA_DATE}" -o /out/abra ./cmd/abra

FROM debian:bookworm-slim AS runtime
WORKDIR /app
ENV NODE_ENV=production
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git openssh-client \
    && rm -rf /var/lib/apt/lists/*
RUN useradd --system --home /app --shell /usr/sbin/nologin abra
COPY --from=build /out/abra-api /app/abra-api
COPY --from=build /out/abra-worker /app/abra-worker
COPY --from=build /out/abra-migrate /app/abra-migrate
COPY --from=build /out/abra /app/abra
COPY migrations ./migrations
USER abra
EXPOSE 8080
CMD ["/app/abra-api"]
