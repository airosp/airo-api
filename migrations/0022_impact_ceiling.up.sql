-- O tecto de impacto: até onde o corpo pode bater no chão.
--
-- Quem tem joelhos maus, quem mora num primeiro andar com vizinhos por baixo,
-- quem está a recomeçar depois de uma lesão — a app propunha-lhes polichinelos
-- e burpees na mesma. A resposta dessas pessoas não é reclamar; é deixar de
-- abrir a app.
--
-- No perfil e não no objectivo: não é uma meta, é uma condição de quem treina,
-- e vale para todos os planos que essa pessoa venha a ter.
--
-- `NULL` quer dizer "sem tecto", que é o caso da maioria — e é diferente de
-- `'high'`, que seria a pessoa a escolher o mais alto. A distinção interessa:
-- um dia o motor pode querer saber se alguém já respondeu à pergunta.
ALTER TABLE profile
  ADD COLUMN IF NOT EXISTS max_impact impact_level;

COMMENT ON COLUMN profile.max_impact IS
  'Tecto de impacto nos exercícios propostos. NULL = sem tecto (a maioria).';
