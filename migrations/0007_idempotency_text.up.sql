-- A chave de idempotência passa a ser texto.
--
-- Era `uuid`, e o cabeçalho `Idempotency-Key` é uma **cadeia opaca**: quem o
-- envia escolhe a forma. Uma chave que não fosse um UUID não dava 422 — dava
-- **500**, porque o erro só aparecia ao chegar ao Postgres:
--
--   invalid input syntax for type uuid: "treino_1789279" (SQLSTATE 22P02)
--
-- Um cliente com identificadores próprios ficava de fora sem perceber porquê, e
-- o servidor dizia "a culpa é nossa" quando a culpa não era de ninguém.
--
-- `USING` converte o que já lá está sem perder nada: um uuid é texto válido.
ALTER TABLE workout_session
    ALTER COLUMN idempotency_key TYPE text USING idempotency_key::text;

ALTER TABLE nutrition_log
    ALTER COLUMN idempotency_key TYPE text USING idempotency_key::text;
