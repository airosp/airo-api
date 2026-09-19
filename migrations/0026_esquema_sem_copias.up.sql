-- Duas tabelas que o modelo deixou para trás, e que valia mais tirar do que
-- encher.
--
-- Um esquema que anuncia armazenamento que ninguém usa é uma promessa falsa:
-- quem o lê para perceber o sistema conclui que o ciclo alimentar e os riscos
-- se guardam aqui, e vai procurar a eles quando precisar deles. Encontra
-- tabelas vazias e não sabe se é um defeito ou uma decisão.
--
-- ⚠️ **Nenhuma das duas perdeu informação nenhuma** — a informação vive noutro
-- sítio, e num sítio só.

-- `nutrition_cycle` — o ciclo não é uma entidade; é o período em que uma
-- estratégia esteve de pé.
--
-- Está em `nutrition_strategy.effective_from` / `effective_to`, e é dali que o
-- `AssessCycle` o lê. Uma segunda data aqui podia discordar dessa, e duas
-- respostas para "quando é que este ciclo começou" é o mesmo que nenhuma.
-- Nunca recebeu uma linha em nove meses de esquema — o comentário em
-- `goal_repo.go` já o dizia antes de esta migração existir.
DROP TABLE IF EXISTS nutrition_cycle;

-- `risk` — os riscos já estão guardados, dentro da avaliação que os produziu.
--
-- `assessment.snapshot` é jsonb e leva `risks` inteiros: tipo, nível,
-- confiança, as evidências que os sustentam e a recomendação. O comentário da
-- própria coluna diz porquê — «guardado inteiro porque sem ele não há como
-- explicar à pessoa porque é que o plano mudou».
--
-- Copiá-los para cá era partir um risco em duas metades — as evidências a
-- ficar no jsonb, o tipo e a gravidade aqui — e ter de as manter de acordo.
-- Além disso `risk_type` e o `severity` nunca acompanharam o motor: ele emite
-- `no_response`, que este enum não conhece, e níveis `low/medium/high` que não
-- são `info/warning/critical`. Traduzir entre os dois a cada escrita é onde
-- nascem as divergências silenciosas.
DROP TABLE IF EXISTS risk;
DROP TYPE IF EXISTS risk_type;
DROP TYPE IF EXISTS severity;
