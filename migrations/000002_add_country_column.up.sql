-- migrations/000002_add_country_column.up.sql
-- Добавляем столбец country в таблицу movies
ALTER TABLE movies ADD COLUMN country VARCHAR(100);

-- Делаем поле обязательным после заполнения данных
ALTER TABLE movies ALTER COLUMN country SET NOT NULL;

-- Создаем индекс на столбец country для оптимизации поиска по стране
CREATE INDEX idx_movies_country ON movies(country);

-- Создаем составной индекс для поиска по стране и году
CREATE INDEX idx_movies_country_year ON movies(country, year);