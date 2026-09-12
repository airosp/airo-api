# ── compilação ───────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS build
WORKDIR /src

# As dependências primeiro: muda menos que o código, e a camada fica em cache.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
ARG VERSION=dev
# CGO desligado para o binário correr na imagem sem libc.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/airo-api ./cmd/api

# ── execução ─────────────────────────────────────────────────────────────────
FROM alpine:3.20
# ca-certificates para falar com a Meta; tzdata porque o dia é local ao
# utilizador e sem fusos o Clock devolve o dia errado.
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 airo
COPY --from=build /out/airo-api /usr/local/bin/airo-api
COPY migrations /migrations
USER airo
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/usr/local/bin/airo-api"]
