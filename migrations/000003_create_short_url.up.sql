-- migrations/000001_create_movies_table.up.sql
-- Создание таблицы фильмов
CREATE TABLE url_srv (
    id SERIAL PRIMARY KEY,
    origin text NOT NULL,
    short text NOT NULL
);
