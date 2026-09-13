-- O diário alimentar passa a viver no servidor.
--
-- `client_id` é o identificador que o telemóvel já dava ao registo. Existe
-- porque o registo nasce offline: a pessoa come, regista, e a rede aparece
-- depois. Sem ele, cada tentativa de envio criaria uma linha nova, e um
-- almoço registado numa rede fraca aparecia três vezes.
--
-- O índice é único por pessoa e parcial: registos criados no servidor (que
-- ainda não existem) ficam de fora em vez de colidirem todos em NULL.
ALTER TABLE nutrition_log
    ADD COLUMN client_id       text,
    ADD COLUMN photo_thumb_url text;

CREATE UNIQUE INDEX nutrition_log_client
    ON nutrition_log (user_id, client_id)
    WHERE client_id IS NOT NULL;
