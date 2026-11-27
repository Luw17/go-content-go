FROM golang:1.23-alpine AS build
ARG VERSION=2.1.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /runtime/data /runtime/uploads /runtime/backups /runtime/plugins \
    && chmod 0750 /runtime/data /runtime/uploads /runtime/backups \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /runtime/gcg ./cmd/server \
    && ln -s /app/gcg /runtime/server

FROM scratch
WORKDIR /app
COPY --from=build --chown=65532:65532 /runtime/ /app/
USER 65532:65532
ENV GCG_ADDR=:8080 \
    GCG_DB=/app/data/go-content-go.db \
    GCG_UPLOADS=/app/uploads \
    GCG_BACKUPS=/app/backups \
    GCG_THEMES=/app/themes \
    GCG_PLUGINS=/app/plugins
EXPOSE 8080
VOLUME ["/app/data", "/app/uploads", "/app/backups", "/app/themes", "/app/plugins"]
ENTRYPOINT ["/app/gcg"]
