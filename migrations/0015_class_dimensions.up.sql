-- As dimensões do vídeo.
--
-- Sem elas, o cliente só descobre a forma do vídeo depois de o carregar — e
-- até lá adivinha pela miniatura. Numa aula solta isso é um acerto que ninguém
-- vê; numa playlist é a página a reorganizar-se a cada troca, porque as aulas
-- não têm todas a mesma proporção (medidas: 1,778:1 e 1,897:1 no catálogo de
-- hoje) e a casca seguia a de cada uma.
--
-- Aqui o cliente sabe a forma **antes** de carregar, e reserva o espaço certo.
ALTER TABLE workout_class
    ADD COLUMN video_width  integer CHECK (video_width  IS NULL OR video_width  > 0),
    ADD COLUMN video_height integer CHECK (video_height IS NULL OR video_height > 0);

-- Nulas de propósito: uma aula carregada antes desta migração não tem medida, e
-- inventar-lhe 16:9 seria escrever um palpite numa coluna de factos. O cliente
-- trata a ausência como "descobre ao carregar", que é o que já fazia.
