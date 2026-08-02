-- migrations/000002_add_country_column.down.sql
-- Откат создания индексов на столбец country
DROP INDEX IF EXISTS idx_movies_country_year;
DROP INDEX IF EXISTS idx_movies_country;
-- Откат добавления столбца country
ALTER TABLE movies DROP COLUMN IF EXISTS country;