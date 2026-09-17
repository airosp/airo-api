-- Os especialistas, e quem os convidou.
--
-- Viviam só no cliente, em `constants/specialists.ts`: a pessoa escolhia a Ana
-- no assistente e isso nunca saía do telemóvel. Reinstalar apagava a escolha, e
-- do lado do servidor um especialista era um nome escrito à mão no campo
-- `specialist` das aulas — texto, sem nada que garantisse que existia.
--
-- A decisão D15 previa isto: «o especialista é texto enquanto o catálogo viver
-- no cliente; quando subir para a base, passa a chave».
CREATE TYPE specialist_role AS ENUM ('trainer', 'nutritionist', 'physio', 'coach');

CREATE TABLE specialist (
    -- O slug é a chave: é o que as aulas já guardam e o que o cliente conhece.
    id            text PRIMARY KEY,
    name          text NOT NULL,
    role          specialist_role NOT NULL,
    headline      text NOT NULL,
    bio           text NOT NULL DEFAULT '',
    tags          text[] NOT NULL DEFAULT '{}',
    -- Prova social. `numeric(2,1)` porque 4.9 é uma nota, não uma medição.
    rating        numeric(2,1) NOT NULL DEFAULT 0,
    clients       integer NOT NULL DEFAULT 0,
    response_time text NOT NULL DEFAULT '',
    initials      text NOT NULL DEFAULT '',
    -- Duas cores para o avatar: evita depender de fotografias que não existem.
    gradient      text[] NOT NULL DEFAULT '{}',
    -- Objectivos para os quais aparece com o selo "sugerido para ti".
    recommended_for text[] NOT NULL DEFAULT '{}',
    active        boolean NOT NULL DEFAULT true
);

-- Quem convidou quem.
--
-- Um por papel, e é o que a app já assume: dois treinadores a assinar o mesmo
-- plano é a confusão que o ecrã de escolha existe para evitar. A restrição
-- garante-o na base em vez de o deixar à boa vontade do cliente.
CREATE TABLE user_specialist (
    user_id      uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    specialist_id text NOT NULL REFERENCES specialist(id) ON DELETE CASCADE,
    role         specialist_role NOT NULL,
    invited_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, specialist_id),
    UNIQUE (user_id, role)
);

-- Agora que a tabela existe, a aula aponta para ela a sério.
--
-- `NOT VALID` de propósito: valida o que entrar de agora em diante sem varrer o
-- que já lá está. As quinze aulas de hoje apontam para slugs que o seed carrega
-- na mesma transacção, mas uma migração que rebenta por causa de uma linha
-- antiga é uma migração que ninguém corre.
ALTER TABLE workout_class
  ADD CONSTRAINT workout_class_specialist_fk
  FOREIGN KEY (specialist) REFERENCES specialist(id) NOT VALID;
