ALTER TABLE admin_member
    ADD COLUMN password_hash  text,
    ADD COLUMN totp_secret    bytea,
    ADD COLUMN totp_enabled   boolean NOT NULL DEFAULT false,
    ADD COLUMN invite_hash    bytea UNIQUE,
    ADD COLUMN invite_expires timestamptz;

DELETE FROM admin_challenge;
ALTER TABLE admin_challenge DROP COLUMN code_hash;
