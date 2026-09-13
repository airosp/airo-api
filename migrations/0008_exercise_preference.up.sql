-- Preferências de exercício: o que a pessoa quer sempre, o que nunca quer, e os
-- números que fixou.
--
-- Viviam só no telemóvel. A consequência era concreta: o servidor montava a
-- sessão sem essas escolhas, o Modo Foco reparava que o pacote descrevia outro
-- treino e voltava ao motor local. Quem personalizava o plano deixava de
-- receber o que o servidor decide.
--
-- Uma tabela e não três colunas no perfil: são listas com significados
-- diferentes, e a prescrição tem números próprios. Uma linha por par
-- (utilizador, exercício) com o papel a dizer qual é qual.
CREATE TYPE exercise_preference_kind AS ENUM ('pinned', 'excluded', 'prescribed');

CREATE TABLE exercise_preference (
    user_id     uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    exercise_id text NOT NULL,
    kind        exercise_preference_kind NOT NULL,

    -- Só para 'prescribed'. Um exercício sem séries não é um exercício —
    -- invariante 13.
    sets        smallint CHECK (sets IS NULL OR sets >= 1),
    -- Repetições por série, ou segundos quando a medida é tempo.
    target      smallint CHECK (target IS NULL OR target >= 1),

    updated_at  timestamptz NOT NULL DEFAULT now(),

    -- Fixar e excluir o mesmo exercício é contraditório; a chave não o impede,
    -- mas o serviço resolve-o antes de escrever. O que a chave garante é que
    -- não há duas opiniões do mesmo tipo sobre o mesmo exercício.
    PRIMARY KEY (user_id, exercise_id, kind),

    -- Uma prescrição sem números não prescreve nada.
    CONSTRAINT prescricao_tem_numeros CHECK (
        kind <> 'prescribed' OR (sets IS NOT NULL AND target IS NOT NULL)
    )
);

CREATE INDEX exercise_preference_user ON exercise_preference (user_id);
