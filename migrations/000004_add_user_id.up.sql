-- migrations/000004...discovery_registry

CREATE TABLE if not exists usr (
    id SERIAL PRIMARY KEY,
    usr_name text UNIQUE NOT NULL,
    paswd text NOT NULL default '1'
);

-- Добавляем столбец user_id в таблицу url_srv
ALTER TABLE url_srv ADD COLUMN usr_id int4;
ALTER TABLE url_srv ADD COLUMN deleted BOOLEAN default FALSE;

ALTER TABLE url_srv ADD CONSTRAINT usr_id_fkey FOREIGN KEY (usr_id) REFERENCES usr(id) ON UPDATE CASCADE ON DELETE CASCADE;
