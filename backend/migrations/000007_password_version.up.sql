ALTER TABLE links ADD COLUMN password_version bigint NOT NULL DEFAULT 0 CHECK (password_version >= 0);
