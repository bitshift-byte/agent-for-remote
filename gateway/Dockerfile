FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/super-agent-gateway ./cmd/gateway

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/super-agent-gateway /usr/local/bin/super-agent-gateway

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/super-agent-gateway"]
