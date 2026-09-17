-- As edições ao treino do dia.
--
-- A pessoa abre o treino de hoje, tira o agachamento porque o joelho dói, troca
-- a prancha por outra coisa e sobe as séries da remada. Essas três decisões
-- viviam só no telemóvel. Reinstalar, ou abrir a conta noutro aparelho, punha
-- lá o treino que o motor propõe — com o agachamento de volta.
--
-- Guarda-se **o dia inteiro num documento**, e não uma linha por alteração. As
-- edições só fazem sentido juntas (tirar A e pôr B é uma troca, não dois
-- factos), o cliente já as tem nessa forma, e mandar o conjunto faz de reenviar
-- uma operação inofensiva: o mesmo dia mandado duas vezes dá o mesmo dia.
--
-- Por dia e não por plano: ao virar o dia, o treino volta ao que o motor
-- propõe. É isso que a app faz e é isso que as pessoas esperam — o joelho de
-- ontem não manda no treino de amanhã.
CREATE TABLE IF NOT EXISTS session_edit (
  user_id    uuid        NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  local_day  date        NOT NULL,
  -- `{removed, added, swapped, tuned}` — a forma que o cliente já tem.
  -- Sem esquema apertado de propósito: isto é a memória de um ecrã, não um
  -- facto do treino. O que ficou feito grava-se noutro sítio, em `session`.
  edits      jsonb       NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, local_day)
);

-- Ler é sempre "os últimos dias desta pessoa": a chave primária já serve.
