FROM golang:1.26.5-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kubepilot-core ./cmd/kubepilot-core

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S aiops \
    && adduser -S aiops -G aiops

WORKDIR /app

COPY --from=builder /out/kubepilot-core /usr/local/bin/kubepilot-core
COPY config.docker.yaml /app/config.yaml
COPY scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

RUN mkdir -p /data \
    && chmod 0755 /usr/local/bin/docker-entrypoint.sh \
    && chown -R aiops:aiops /app /data

USER aiops

ENV AIOPS_CONFIG=/app/config.yaml

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/kubepilot-core"]
