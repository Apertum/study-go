-- migrations/000001_create_movies_table.up.sql
-- Создание таблицы фильмов
CREATE TABLE url_srv (
    id SERIAL PRIMARY KEY,
    uuid INT  NOT NULL,
    original_url text UNIQUE NOT NULL,
    short_url text NOT NULL
);