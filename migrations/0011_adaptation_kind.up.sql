-- O enum era anterior ao porte do motor de jornada, e fala outra língua.
--
-- O esquema descrevia acções finas — `reduce_frequency`, `deload` — e o motor
-- decide categorias: `reduce_load`, `review_goal`. Mapear umas nas outras
-- inventava uma especificidade que a decisão não tem: "aliviar o plano" pode
-- ser menos dias **ou** menos minutos, e é o `payload` que o diz.
--
-- Os valores antigos ficam. Não há linhas a usá-los — nada escrevia nesta
-- tabela até agora — mas apagá-los seria apagar o vocabulário de uma decisão
-- futura por conveniência de hoje.
ALTER TABLE adaptation ALTER COLUMN kind TYPE text;
DROP TYPE adaptation_kind;
CREATE TYPE adaptation_kind AS ENUM (
    -- Do motor de jornada, que é quem decide.
    'maintain','reduce_load','increase_load','simplify','extend_timeframe','review_goal',
    -- Do esquema original, para uma acção mais fina quando existir quem a decida.
    'reduce_frequency','increase_frequency','reduce_duration','increase_duration',
    'reduce_intensity','increase_intensity','deload','extend_timeline','hold',
    'calorie_up','calorie_down','recalibrate'
);
ALTER TABLE adaptation ALTER COLUMN kind TYPE adaptation_kind USING kind::adaptation_kind;
