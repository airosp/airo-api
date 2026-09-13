-- Dispensar não é aplicar, e não é ficar por decidir.
--
-- A tabela só sabia distinguir "aplicada" de "por aplicar". Sem um terceiro
-- estado, uma proposta recusada voltava a aparecer no dia seguinte — e uma
-- sugestão que reaparece depois de dispensada deixa de ser sugestão.
ALTER TABLE adaptation ADD COLUMN dismissed_at timestamptz;

-- A jornada a que a adaptação pertence, para as listar sem passar pelo
-- assessment. Deriva-se dele, mas a consulta mais frequente é "o que está
-- pendente nesta jornada" e fazê-la por junção em cada leitura é caro.
ALTER TABLE adaptation ADD COLUMN journey_id uuid REFERENCES journey(id) ON DELETE CASCADE;

CREATE INDEX adaptation_pendentes ON adaptation (journey_id)
    WHERE applied_at IS NULL AND dismissed_at IS NULL;
