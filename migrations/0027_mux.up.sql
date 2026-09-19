-- As aulas passam a viver no Mux.
--
-- ⚠️ Até aqui uma aula era **um endereço de ficheiro** — um MP4 do acervo de
-- stock, servido por quem o alojasse. Isso chega para um placeholder e não
-- chega para o produto: um MP4 único é a mesma resolução para quem está na
-- fibra e para quem está a 3G no ginásio, não tem como ser protegido (quem
-- tem o endereço tem o vídeo, para sempre e para toda a gente), e o trabalho
-- de um profissional que filma uma aula não pode ficar num endereço que se
-- copia e se partilha.
--
-- O Mux resolve as três: entrega em HLS com vários débitos, dá um
-- identificador de reprodução em vez de um ficheiro, e assina o acesso com um
-- token que expira.
--
-- **O endereço deixa de ser guardado.** O que se guarda é o identificador; o
-- endereço monta-se a cada pedido, porque com política assinada ele leva um
-- token com prazo e um endereço guardado seria um endereço morto.
ALTER TABLE workout_class
    -- O identificador de reprodução: é dele que sai o `.m3u8` e a miniatura.
    ADD COLUMN mux_playback_id text,
    -- O do recurso, para o webhook saber que aula actualizar e para se poder
    -- pedir ao Mux o que é feito dele.
    ADD COLUMN mux_asset_id text,
    /*
     * A política de reprodução, que decide se o endereço leva token.
     *
     * `signed` por omissão, e não `public`: uma aula pública é uma aula que
     * qualquer pessoa vê sem ter conta, e o engano por omissão tem de ser o
     * que fecha, não o que abre.
     */
    ADD COLUMN mux_policy text NOT NULL DEFAULT 'signed'
        CHECK (mux_policy IN ('public', 'signed')),
    -- Um recurso no Mux só se vê quando acaba de ser processado. Até lá a aula
    -- existe, tem ficha, e não tem vídeo — e é melhor dizê-lo do que servir um
    -- leitor que gira para sempre.
    ADD COLUMN mux_ready boolean NOT NULL DEFAULT false;

-- O endereço directo passa a ser opcional: as aulas do Mux não têm nenhum.
ALTER TABLE workout_class ALTER COLUMN video_url SET DEFAULT '';

-- Uma aula **publicada** tem de ter uma fonte de vídeo.
--
-- ⚠️ A regra é sobre o publicar, e não sobre o existir: uma aula nasce sem
-- vídeo nenhum. O produtor escreve a ficha, pede um destino de envio, e o
-- ficheiro demora minutos a ser processado — exigir a fonte à nascença tornava
-- o próprio fluxo de upload impossível. O que não pode acontecer é uma aula
-- chegar à lista sem ter o que tocar.
ALTER TABLE workout_class
    ADD CONSTRAINT class_publicada_tem_video
    CHECK (NOT published OR mux_playback_id IS NOT NULL OR video_url <> '');

-- Procurar a aula pelo recurso é o que o webhook faz a cada notificação.
CREATE UNIQUE INDEX IF NOT EXISTS class_mux_asset ON workout_class (mux_asset_id)
    WHERE mux_asset_id IS NOT NULL;
