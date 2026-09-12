#!/usr/bin/env bash
# Deploy da API no EasyPanel.
#
# Duas coisas que custaram uma tarde e estão aqui para não voltarem a custar:
#
#  1. O painel **não** aceita mutações pelo domínio público. Pelo
#     `infra.savanapoint.com` todas devolvem "Invariant failed" — até para um
#     projeto que não existe. Directamente no painel, funcionam.
#  2. Um `{}` devolvido em menos de 30 s é um não-evento: já havia um deploy a
#     correr e este foi descartado em silêncio. A construção a sério demora
#     70-90 s. Por isso repete-se até uma pegar.
#
# Uso:  EASYPANEL_TOKEN=… scripts/deploy.sh [tentativas]
set -euo pipefail

PANEL="${EASYPANEL_PANEL:-http://72.60.95.199:3000}"
PROJECT="${EASYPANEL_PROJECT:-savanapoint}"
SERVICE="${EASYPANEL_SERVICE:-airo-server-api}"
BASE="${AIRO_PUBLIC_URL:-https://airo-api.savanapoint.com}"
TRIES="${1:-5}"

: "${EASYPANEL_TOKEN:?falta EASYPANEL_TOKEN — está no .env da raiz}"

for i in $(seq 1 "$TRIES"); do
  start=$(date +%s)
  printf '[%s] deploy %d/%d… ' "$(date +%T)" "$i" "$TRIES"
  curl -sS -m 900 -X POST \
    -H "Authorization: Bearer $EASYPANEL_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"json\":{\"projectName\":\"$PROJECT\",\"serviceName\":\"$SERVICE\",\"forceRebuild\":true}}" \
    "$PANEL/api/trpc/services.app.deployService" >/dev/null
  took=$(( $(date +%s) - start ))
  echo "${took}s"

  if [ "$took" -ge 30 ]; then
    echo "construção feita; à espera que o contentor troque"
    for _ in $(seq 1 20); do
      sleep 10
      ready=$(curl -s -m 10 -o /dev/null -w '%{http_code}' "$BASE/readyz" || true)
      [ "$ready" = "200" ] && { echo "pronto: /readyz 200"; exit 0; }
      echo "  /readyz $ready"
    done
    echo "construiu mas o /readyz não chegou a 200" >&2
    exit 1
  fi

  echo "  (descartado — outro deploy a correr; nova tentativa em 30 s)"
  sleep 30
done

echo "nenhuma tentativa pegou ao fim de $TRIES" >&2
exit 1
