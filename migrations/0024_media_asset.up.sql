-- As imagens que já foram encontradas, para não se voltar a procurar.
--
-- ⚠️ Cada telemóvel resolvia as suas: abrir a app pela primeira vez eram
-- dezenas de pedidos ao acervo para descobrir o que toda a gente já tinha
-- descoberto. A Pexels dá 200 pedidos por hora à conta inteira — não por
-- pessoa —, e uma lista de refeições monta dezenas de miniaturas. Com alguma
-- gente a usar a app ao mesmo tempo, as fotografias simplesmente deixavam de
-- aparecer, sem nada no ecrã a explicar porquê.
--
-- Aqui uma imagem é encontrada **uma vez** e serve todos. E quem não tem rede
-- para falar com a Pexels — ou está num país onde ela é lenta — recebe na mesma
-- o endereço, porque quem falou com ela fomos nós.
CREATE TABLE IF NOT EXISTS media_asset (
  -- Do que é a imagem: `food:chicken`, `label:pastel de nata`.
  -- Texto e não duas colunas: o prefixo diz o tipo e mantém a chave única sem
  -- inventar um enum que muda a cada novo sítio onde se queira uma imagem.
  subject     text PRIMARY KEY,
  url         text NOT NULL,
  width       int,
  height      int,
  -- Quem a fez. A Pexels pede atribuição, e uma imagem sem autor é uma imagem
  -- que não se pode publicar.
  credit      text,
  provider    text NOT NULL DEFAULT 'pexels',
  fetched_at  timestamptz NOT NULL DEFAULT now()
);

-- Uma imagem que não se encontrou também é uma resposta: guarda-se com `url`
-- vazio para não se voltar a procurar a cada ecrã. Ver o `ttlDeFalha` no
-- servidor para quanto tempo se acredita nela.
COMMENT ON COLUMN media_asset.url IS
  'Endereço da imagem. Vazio = procurou-se e não havia.';
