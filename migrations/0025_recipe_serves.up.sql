-- Quantas porções a receita rende.
--
-- ⚠️ O catálogo de receitas vive em `internal/engine/nutrition/data/recipes.json`
-- e cada receita diz `serves`. A tabela não tinha onde o guardar, e por isso
-- semear o catálogo perdia a informação — uma matapa que rende duas porções
-- entrava igual a uma que rende uma. Sem isto, os gramas por pessoa saem ao
-- dobro, e o erro só aparece no prato.
--
-- `1` por omissão porque é o que quase todas rendem, e porque uma receita sem
-- resposta é uma receita para uma pessoa — não uma receita sem porções.
ALTER TABLE recipe ADD COLUMN IF NOT EXISTS serves smallint NOT NULL DEFAULT 1
  CHECK (serves > 0);
