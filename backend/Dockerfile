# ---- build ----------------------------------------------------------------
FROM golang:1.24-alpine AS build

WORKDIR /src

# Dependencies first so they stay cached across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is not needed (pgx is pure Go); a static binary keeps the final image tiny.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed   ./cmd/seed

# ---- runtime --------------------------------------------------------------
FROM alpine:3.21

# Certificates are needed to reach a managed Postgres over TLS.
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 app

COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/seed   /usr/local/bin/seed

USER app
EXPOSE 8080

# Migrations are embedded in the binary and run automatically on boot.
ENTRYPOINT ["/usr/local/bin/server"]
