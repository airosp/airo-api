-- A lista de compras da semana.
--
-- O plano alimentar diz o que comer todos os dias e ninguém o conseguia levar
-- para o mercado. Quem seguia o plano abria sete ecrãs de refeição e ia
-- somando de cabeça — ou não seguia.
--
-- **Guarda-se o que a pessoa decidiu, não a lista.** A lista sai do plano e o
-- plano já é do motor: gravá-la seria uma segunda cópia a envelhecer sozinha,
-- e mudar de estratégia a meio da semana deixava no servidor uma lista de
-- comida que já ninguém ia comer. O que não se consegue voltar a calcular é o
-- que foi riscado ("isto já tenho em casa") e o que foi acrescentado à mão
-- (o sabão, o pão, a coisa que o motor não sabe) — e é isso que fica.
CREATE TABLE IF NOT EXISTS shopping_list (
  user_id    uuid        NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  -- A segunda-feira da semana. Por semana e não por dia: é assim que se vai ao
  -- mercado.
  week_start date        NOT NULL,
  -- `{checked: [...], extras: [{id, label, note}]}`.
  state      jsonb       NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, week_start)
);
