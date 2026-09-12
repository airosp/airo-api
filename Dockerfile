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

# O EasyPanel encaminha para a porta 80. O valor por omissão da aplicação
# continua a ser :8080 para desenvolvimento local — aqui é o contentor que diz
# onde escuta, e os dois lados passam a concordar.
ENV AIRO_HTTP_ADDR=:80
EXPOSE 80
# Sem HEALTHCHECK no Dockerfile, de propósito.
#
# /readyz responde 503 enquanto o esquema estiver atrasado — que é o sinal
# certo. Mas um HEALTHCHECK sobre ele torna o contentor "não saudável", o
# encaminhador deixa de lhe mandar tráfego, e o 503 que explicava o problema
# vira um 502 que não explica nada. O serviço ficava invisível justamente
# quando é preciso perguntar-lhe o que se passa.
#
# Quem decide encaminhar é o EasyPanel, com o seu próprio teste sobre /readyz.
ENTRYPOINT ["/usr/local/bin/airo-api"]
