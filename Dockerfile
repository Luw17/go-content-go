FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /runtime/data /runtime/uploads /runtime/backups /runtime/plugins \
    && chmod 0750 /runtime/data /runtime/uploads /runtime/backups \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /runtime/server ./cmd/server

FROM scratch
WORKDIR /app
COPY --from=build --chown=65532:65532 /runtime/ /app/
COPY --from=build --chown=65532:65532 /src/themes/default/ /app/themes/default/
COPY --from=build --chown=65532:65532 /src/themes/editorial/ /app/themes/editorial/
USER 65532:65532
ENV GO_CONTENT_GO_ADDR=:8080 \
    GO_CONTENT_GO_DB=/app/data/go-content-go.db \
    GO_CONTENT_GO_UPLOADS=/app/uploads \
    GO_CONTENT_GO_BACKUPS=/app/backups \
    GO_CONTENT_GO_THEMES=/app/themes \
    GO_CONTENT_GO_PLUGINS=/app/plugins
EXPOSE 8080
VOLUME ["/app/data", "/app/uploads", "/app/backups", "/app/themes", "/app/plugins"]
ENTRYPOINT ["/app/server"]
