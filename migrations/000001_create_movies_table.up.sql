-- migrations/000001_create_movies_table.up.sql
-- Создание таблицы фильмов
CREATE TABLE movies (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    year INTEGER NOT NULL
);

-- Базовый индекс для поиска по названию
CREATE INDEX idx_movies_name ON movies(name);

-- Индекс для поиска по году
CREATE INDEX idx_movies_year ON movies(year);