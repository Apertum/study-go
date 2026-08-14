-- migrations/000001_create_movies_table.down.sql
-- Откат создания таблицы фильмов
DROP INDEX IF EXISTS idx_movies_year;
DROP INDEX IF EXISTS idx_movies_name;
DROP TABLE IF EXISTS movies;