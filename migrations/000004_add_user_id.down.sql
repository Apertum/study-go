-- migrations/000004...
-- Откат добавления столбца country
ALTER TABLE url_srv DROP COLUMN IF EXISTS usr_id;

DROP TABLE IF EXISTS usr;