-- A escolha da pessoa dentro de uma refeição do dia.
--
-- O plano do dia é uma proposta do servidor; trocar uma refeição é uma decisão
-- de quem a vai comer. Sem isto, a decisão vivia no telemóvel e desaparecia ao
-- mudar de aparelho — e o plano do servidor voltava a propor o que já tinha
-- sido recusado.
--
-- Guarda-se a **variante**, não os alimentos: os alimentos derivam-se dela com
-- o mesmo motor dos dois lados, e guardar a lista faria o plano deixar de
-- acompanhar uma mudança no catálogo.
CREATE TABLE meal_preference (
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    local_day  date NOT NULL,
    slot       meal_slot NOT NULL,
    -- Sobe a cada troca. Zero seria "sem troca", e aí a linha não existiria.
    variant    integer NOT NULL CHECK (variant >= 1),
    updated_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, local_day, slot)
);

CREATE INDEX meal_preference_user_day ON meal_preference (user_id, local_day);
