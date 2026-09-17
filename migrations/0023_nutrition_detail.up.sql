-- Quanto da nutrição se mostra.
--
-- A app mostra calorias, proteína, hidratos e gordura a toda a gente. Para quem
-- veio criar o hábito de comer melhor, quatro números por refeição não são
-- ajuda: são um teste que ela não sabia que ia ter, e o que faz é fechar a app.
--
-- `simple`  — refeições, horas e o que já comeu. Sem macros.
-- `detailed` — tudo, como está hoje.
--
-- O valor por omissão é `detailed` de propósito: mudar o que as pessoas já usam
-- sem elas pedirem é tirar-lhes o chão. Quem quiser menos, escolhe.
CREATE TYPE nutrition_detail AS ENUM ('simple', 'detailed');

ALTER TABLE profile
  ADD COLUMN IF NOT EXISTS nutrition_detail nutrition_detail NOT NULL DEFAULT 'detailed';

COMMENT ON COLUMN profile.nutrition_detail IS
  'Quanto da nutrição se mostra: só refeições, ou também os macros.';
