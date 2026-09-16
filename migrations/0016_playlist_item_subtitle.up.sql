-- A linha que explica o lugar da aula na lista.
--
-- "Aquecimento · Cardio", "Treino principal · Força", "Recuperação ·
-- Mobilidade". Não se deduz do foco da aula: a mesma aula de cardio é
-- aquecimento numa lista e trabalho principal noutra — o papel é da sequência,
-- não do vídeo.
--
-- Texto escrito, como as outras etiquetas que este servidor manda: quem curou
-- a lista escreve-o, e o cliente desenha-o sem o compor.
ALTER TABLE playlist_item ADD COLUMN subtitle text NOT NULL DEFAULT '';
