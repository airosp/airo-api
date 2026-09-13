-- Aulas gravadas: um especialista a dar treino, filmado por nós.
--
-- É outra coisa do que o plano gerado, e o esquema tem de o dizer. Numa aula
-- **o vídeo lidera**: quem decidiu os exercícios, as séries e os descansos foi
-- quem a filmou. O motor não a monta; serve-a.
--
-- Por isso não há aqui `exercise_prescription` nem passos. Uma aula não é uma
-- sessão montada — é uma sessão que já aconteceu uma vez, à frente de uma
-- câmara, e que se repete.
CREATE TABLE workout_class (
    id          text PRIMARY KEY,
    title       text NOT NULL,
    -- Slug do especialista, do catálogo de `constants/specialists.ts`. Texto e
    -- não chave estrangeira enquanto os especialistas viverem no cliente — ver
    -- docs/decisoes.md D15.
    specialist  text NOT NULL,
    focus       session_focus NOT NULL,
    level       experience NOT NULL,

    -- A duração é a do vídeo, e é ela que conta como tempo planeado: numa aula,
    -- o plano é o que a Ana filmou.
    duration_seconds integer NOT NULL CHECK (duration_seconds > 0),
    kcal             integer NOT NULL DEFAULT 0,

    -- O endereço do vídeo. Simples de propósito: a Cloudinary é o alojamento de
    -- hoje, e trocá-lo não devia exigir migração.
    video_url     text NOT NULL,
    thumbnail_url text,

    summary     text NOT NULL DEFAULT '',
    muscles     text[] NOT NULL DEFAULT '{}',
    equipment   text[] NOT NULL DEFAULT '{}',

    -- Publicada: as que ainda estão a ser produzidas não aparecem a ninguém.
    published   boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX class_published ON workout_class (published, focus, level);

-- De onde veio a sessão gravada.
--
-- Sem isto, uma aula e um treino do plano ficam indistinguíveis no histórico —
-- e são coisas diferentes: uma adapta-se à pessoa, a outra é igual para todos.
ALTER TABLE workout_session ADD COLUMN class_id text REFERENCES workout_class(id) ON DELETE SET NULL;
