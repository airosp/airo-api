-- Idempotência das escritas de sessão.
--
-- O cliente treina offline e sincroniza depois — possivelmente duas vezes, se a
-- rede voltar a meio do envio. Sem esta chave, reenviar cria um segundo treino,
-- a sequência salta um dia e a adesão passa a contar sessões que não existiram.
--
-- A chave é gerada no dispositivo e é **por utilizador**: dois telemóveis podem
-- gerar a mesma sem que isso queira dizer a mesma sessão.
--
-- Prescrita em docs/backend/04-arquitetura-go.md e em falta no esquema inicial.
ALTER TABLE workout_session
    ADD COLUMN idempotency_key uuid;

-- Parcial: sessões sem chave continuam a poder repetir-se. Quem treina duas
-- vezes no mesmo dia não está a duplicar nada.
CREATE UNIQUE INDEX workout_session_idem
    ON workout_session (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- O mesmo para o registo alimentar, pela mesma razão.
ALTER TABLE nutrition_log
    ADD COLUMN idempotency_key uuid;

CREATE UNIQUE INDEX nutrition_log_idem
    ON nutrition_log (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
