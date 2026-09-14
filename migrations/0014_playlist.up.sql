-- Playlists: aulas em sequência, com um objetivo.
--
-- Uma aula solta serve quem já sabe o que quer. Uma playlist serve quem tem um
-- objetivo e não sabe por onde começar — e é assim que as aulas vão ser
-- servidas na maioria das vezes.
--
-- São **curadas**, não geradas. Quem produz os vídeos monta a sequência e
-- etiqueta-a; o motor não a compõe. É a mesma decisão que se tomou para as
-- aulas (D15): numa aula o vídeo lidera, e numa playlist lidera a ordem que
-- alguém pensou.
CREATE TABLE playlist (
    id        text PRIMARY KEY,
    title     text NOT NULL,
    summary   text NOT NULL DEFAULT '',
    cover_url text,

    -- A que objetivos serve. Texto e não enum de propósito: os objetivos vivem
    -- no cliente (`strength|fatLoss|muscle|habit`) e um enum obrigaria a uma
    -- migração de cada vez que o produto acrescentasse um.
    goals text[] NOT NULL DEFAULT '{}',
    -- Zonas do corpo, na linguagem de quem treina — "Cintura", "Glúteos".
    -- Serve para procurar e para explicar, nunca para calcular.
    zones text[] NOT NULL DEFAULT '{}',

    level     experience NOT NULL,
    published boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX playlist_publicada ON playlist (published, level);

-- A ordem é o conteúdo.
--
-- `position` na chave primária, e não um `id` próprio: duas aulas na mesma
-- posição da mesma lista não é um erro de dados raro, é uma lista sem ordem —
-- e uma playlist sem ordem não é uma playlist.
CREATE TABLE playlist_item (
    playlist_id text NOT NULL REFERENCES playlist(id) ON DELETE CASCADE,
    position    integer NOT NULL CHECK (position > 0),
    class_id    text NOT NULL REFERENCES workout_class(id) ON DELETE RESTRICT,
    PRIMARY KEY (playlist_id, position)
);

CREATE INDEX playlist_item_aula ON playlist_item (class_id);

-- Feito ou saltado. Não há terceiro estado guardado: "por fazer" é a ausência
-- de linha, e inventar-lhe um valor era ter duas maneiras de dizer o mesmo.
CREATE TYPE playlist_item_status AS ENUM ('done', 'skipped');

-- Por onde cada pessoa vai.
--
-- Saltar é "hoje não": fica a marca, a lista pode dar-se por terminada na
-- mesma, e da próxima vez o item volta a aparecer. Por isso o estado vive aqui
-- e não numa exclusão permanente — essa já existe noutro sítio, para quem diz
-- "nunca mais este".
CREATE TABLE playlist_item_state (
    user_id     uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    playlist_id text NOT NULL,
    position    integer NOT NULL,
    status      playlist_item_status NOT NULL,

    -- Quanto do vídeo foi mesmo visto. É o que permite ao servidor decidir se
    -- contou — o cliente manda segundos, nunca um veredicto.
    watched_seconds integer NOT NULL DEFAULT 0 CHECK (watched_seconds >= 0),

    updated_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, playlist_id, position),
    FOREIGN KEY (playlist_id, position)
        REFERENCES playlist_item (playlist_id, position) ON DELETE CASCADE
);

CREATE INDEX playlist_state_por_pessoa ON playlist_item_state (user_id, playlist_id);
