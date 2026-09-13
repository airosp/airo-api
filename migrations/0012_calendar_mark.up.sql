-- Excepções de um dia concreto, ausências e notas.
--
-- O plano semanal diz o que acontece *todas* as semanas. Isto é a camada por
-- cima: "esta quarta não treino", "estou fora de 12 a 20", "hoje doeu o joelho".
--
-- Viviam só no telemóvel, e a consequência era a mesma da pausa: uma ausência
-- não saía do denominador da adesão, por isso avisar saía mais caro do que
-- desaparecer.
--
-- A precedência é sempre a mesma, e é a que a pessoa espera:
--   excepção do dia  >  ausência  >  padrão semanal
-- Quem marcou "treino" a meio de uma viagem quis mesmo dizer isso.
CREATE TYPE calendar_mark_kind AS ENUM ('workout', 'rest', 'absence', 'note');

CREATE TABLE calendar_mark (
    id         text NOT NULL,
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    kind       calendar_mark_kind NOT NULL,
    day        date NOT NULL,
    -- `until` inclusive. Só as ausências ocupam um intervalo; as outras marcam
    -- um dia, e aí `until` é igual a `day`.
    until      date NOT NULL,
    text       text,
    created_at timestamptz NOT NULL DEFAULT now(),

    -- O identificador nasce no telemóvel, como no diário alimentar: a marca
    -- é feita offline e é ela que lhe dá o nome. Reenviar é mandar a mesma
    -- marca, não uma segunda.
    PRIMARY KEY (user_id, id),

    CONSTRAINT mark_range_ordered CHECK (until >= day),
    -- Só as ausências se estendem. Uma nota de três dias seria três notas.
    CONSTRAINT mark_range_only_absence CHECK (kind = 'absence' OR until = day)
);

CREATE INDEX calendar_mark_by_day ON calendar_mark (user_id, day, until);
