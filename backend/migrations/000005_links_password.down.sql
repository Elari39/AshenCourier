-- 000005 down（严格互逆：up 只加了一列，且没有索引）
ALTER TABLE links DROP COLUMN password_hash;
