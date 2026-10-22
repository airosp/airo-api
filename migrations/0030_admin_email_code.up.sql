-- O painel entra só com o email: um código de seis dígitos chega à caixa de
-- correio e troca-se pela sessão. Sem palavra-passe nem app autenticadora,
-- e sem links de convite: ser membro activo basta para pedir o código.

DELETE FROM admin_challenge;
ALTER TABLE admin_challenge ADD COLUMN code_hash bytea NOT NULL;

ALTER TABLE admin_member
    DROP COLUMN password_hash,
    DROP COLUMN totp_secret,
    DROP COLUMN totp_enabled,
    DROP COLUMN invite_hash,
    DROP COLUMN invite_expires;
