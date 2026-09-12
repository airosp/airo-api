-- A idade quando não há data de nascimento.
--
-- O perfil guarda `birth_date`, e a idade sai dela. Mas a app pergunta "que
-- idade tens?" e não "quando fazes anos" — e converter uma na outra obrigaria a
-- inventar um dia. Um aniversário fabricado fica errado 364 dias por ano, e a 1
-- de Janeiro envelhecia toda a gente de uma vez.
--
-- `birth_date` continua a mandar quando existe: é a informação melhor, e quem a
-- der passa a ter a idade certa no dia certo. Esta coluna é a segunda escolha,
-- não uma cópia — por isso o CHECK garante que é uma idade e não um ano.
ALTER TABLE profile
    ADD COLUMN age_years smallint
        CHECK (age_years IS NULL OR age_years BETWEEN 13 AND 120);
