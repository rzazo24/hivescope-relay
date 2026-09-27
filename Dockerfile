# Etapa de compilación: necesitamos gcc porque el driver de SQLite
# (mattn/go-sqlite3) usa cgo, no es Go puro.
FROM golang:1.27-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /out/hivescope-relay .

# Etapa final: una imagen mínima, sin toolchain de Go, solo el binario ya
# compilado y los certificados TLS del sistema (necesarios para que el relay
# pueda hacer HTTPS hacia la API de Hive).
FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/hivescope-relay ./hivescope-relay

ENV HIVESCOPE_DB_PATH=/app/data/hivescope-relay.sqlite
ENV HIVESCOPE_LISTEN_ADDR=:3334
ENV HIVESCOPE_HIVE_NODE=https://api.hive.blog

EXPOSE 3334
VOLUME ["/app/data"]

ENTRYPOINT ["./hivescope-relay"]
