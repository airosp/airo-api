-- Os alimentos que a pessoa escreve, e as combinações que guarda.
--
-- As duas coisas nasciam no telemóvel e morriam lá. Quem escrevia a receita da
-- avó ou guardava o almoço de terça perdia-as ao reinstalar — e são
-- precisamente o tipo de dado que custa a reintroduzir, porque ninguém se
-- lembra das gramas.

-- ── alimentos próprios ──────────────────────────────────────────────────────
--
-- Tabela à parte do `food`, e não uma coluna `user_id` nele: o catálogo é
-- comum, curado e pesquisável por toda a gente; isto é de uma pessoa só. Juntar
-- os dois obrigava todas as consultas do catálogo a lembrar-se de filtrar — e
-- bastava uma esquecer-se para o arroz de alguém aparecer no plano de outro.
CREATE TABLE custom_food (
    -- O identificador vem do telemóvel: o alimento nasce lá, muitas vezes sem
    -- rede, e reenviá-lo é reenviar o mesmo alimento.
    id         text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    name       text NOT NULL,
    -- Por porção, como a pessoa a escreveu — e não por 100 g como no catálogo.
    -- Converter na entrada era inventar precisão que ela não deu.
    kcal       numeric(6,1) NOT NULL CHECK (kcal >= 0),
    protein_g  numeric(5,1) NOT NULL DEFAULT 0 CHECK (protein_g >= 0),
    carbs_g    numeric(5,1) NOT NULL DEFAULT 0 CHECK (carbs_g >= 0),
    fat_g      numeric(5,1) NOT NULL DEFAULT 0 CHECK (fat_g >= 0),
    serving_g  smallint NOT NULL CHECK (serving_g > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX custom_food_owner ON custom_food (user_id, name);

-- ── favoritas ───────────────────────────────────────────────────────────────
--
-- A tabela já existia desde o início e nunca foi escrita: não havia rota que
-- lhe chegasse. O identificador passa a ser texto pela mesma razão do alimento
-- — quem baptiza a combinação é o telemóvel onde ela foi guardada.
ALTER TABLE favourite_meal ALTER COLUMN id DROP DEFAULT;
ALTER TABLE favourite_meal ALTER COLUMN id TYPE text USING id::text;

CREATE INDEX favourite_meal_owner ON favourite_meal (user_id, slot);
