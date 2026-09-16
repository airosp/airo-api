-- A água: um total por dia, por pessoa.
--
-- Guarda-se o **total do dia** e não cada copo. A app soma de 250 em 250 e o
-- que o ecrã mostra é a soma; guardar vinte eventos para responder sempre à
-- mesma pergunta era guardar o caminho para dar o destino. Quando alguém
-- quiser "a que horas bebeste", isto passa a tabela de eventos e a soma
-- calcula-se — mas hoje ninguém faz essa pergunta.
--
-- O **alvo** não vive aqui: é decidido no servidor a partir do peso e do
-- treino do dia, e já vai no `/v1/nutrition/today`. Um alvo guardado ao lado do
-- consumo ficava desactualizado no dia em que a pessoa mudasse de peso.
CREATE TABLE hydration_day (
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    -- O dia **local** de quem bebe, não o UTC do servidor: às 23h em Maputo já
    -- é o dia seguinte em Londres, e a água era de ontem.
    local_day  date NOT NULL,
    ml         integer NOT NULL CHECK (ml >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, local_day)
);

-- Ler é sempre "os últimos dias desta pessoa".
CREATE INDEX hydration_recent ON hydration_day (user_id, local_day DESC);
