# airo-api

A API da Airo. Go + PostgreSQL.

> **Os motores de decisão são a única implementação que existe.** O cliente
> móvel não processa regras de negócio: recebe valores decididos e desenha-os.
> A fronteira está em [`airo-docs`](https://github.com/airosp/airo-docs),
> `backend/07-fronteira-cliente-servidor.md`, e é verificada por lint no cliente.

## Arrancar

```bash
cp .env.example .env      # preenche AIRO_DATABASE_URL
go run ./cmd/api
curl localhost:8080/healthz
```

`/healthz` diz que o processo está de pé — não toca em dependências, porque um
balanceador que reinicia a API por a base de dados ter piscado transforma uma
falha em duas. `/readyz` é que verifica as dependências.

## Estrutura

```
cmd/api            arranque, wiring, encerramento ordenado
cmd/worker         tarefas periódicas: viragem de ciclo, avanço de fase
internal/
  domain           tipos partilhados, sem dependências
  engine           ⚠️ SEM I/O. Funções puras: recebem um instantâneo, devolvem
                   uma decisão. É o que permite testá-las sem infra-estrutura,
                   e é a propriedade que o porte de TypeScript tem de preservar.
  service          orquestra: lê → chama o motor → escreve
  repository       SQL, uma interface por agregado
  transport/http   router, handlers, middleware, dto, view
  platform         config, postgres, logger, clock
migrations         DDL, .up.sql e .down.sql
```

`handler → service → engine`. O serviço é o único que sabe que existe base de
dados; o motor não sabe.

## Regras que o código tem de cumprir

| | |
|---|---|
| Nenhum `time.Now()` fora de `platform/clock` | O dia é local ao utilizador, e testar a viragem de um ciclo não pode exigir esperar quatro semanas |
| Nenhum limiar de domínio compilado | Não estão validados clinicamente e vão mudar sem nova versão da app |
| O código OTP nunca em registos | `platform/logger` oculta-o, e há um teste que o garante |
| Uma operação de utilizador, uma transação | Uma sessão sem as suas séries é pior do que nenhuma sessão |
| Escritas idempotentes | O cliente treina offline e sincroniza — possivelmente duas vezes |

## Testes

```bash
go test ./...                                   # tudo
go test ./internal/engine/... -run Invariant    # os 15 invariantes
```

Os invariantes vêm de `airo-docs/backend/03-regras-de-negocio.md` §5. Cada um
corresponde a um defeito que já aconteceu.
