-- Справочники. Допустимые значения проверяет сервис по этим таблицам (кэш при
-- старте), поэтому CHECK-ограничений и ENUM-типов нет. id фиксированы: на них
-- ссылается частичный индекс ниже.

-- +goose Up
CREATE TABLE cities (
    id   smallint PRIMARY KEY,
    name text     NOT NULL UNIQUE
);
INSERT INTO cities (id, name) VALUES
    (1, 'Москва'),
    (2, 'Санкт-Петербург'),
    (3, 'Казань');

CREATE TABLE product_types (
    id   smallint PRIMARY KEY,
    name text     NOT NULL UNIQUE
);
INSERT INTO product_types (id, name) VALUES
    (1, 'электроника'),
    (2, 'одежда'),
    (3, 'обувь');

CREATE TABLE reception_statuses (
    id   smallint PRIMARY KEY,
    name text     NOT NULL UNIQUE
);
INSERT INTO reception_statuses (id, name) VALUES
    (1, 'in_progress'),
    (2, 'close');

CREATE TABLE pvz (
    id                uuid        PRIMARY KEY,
    registration_date timestamptz NOT NULL,
    city_id           smallint    NOT NULL REFERENCES cities (id)
);

CREATE TABLE receptions (
    id        uuid        PRIMARY KEY,
    pvz_id    uuid        NOT NULL REFERENCES pvz (id),
    date_time timestamptz NOT NULL,
    status_id smallint    NOT NULL REFERENCES reception_statuses (id)
);
-- Приёмки ПВЗ по дате: последняя открытая приёмка и фильтр GET /pvz.
-- Заодно покрывает внешний ключ pvz_id.
CREATE INDEX receptions_pvz_id_date_time_idx ON receptions (pvz_id, date_time);
-- Не больше одной открытой приёмки (status_id 1 = in_progress) на ПВЗ.
-- Это страховка: основная проверка в сервисе под блокировкой строки ПВЗ.
CREATE UNIQUE INDEX receptions_one_open_per_pvz_idx ON receptions (pvz_id) WHERE status_id = 1;

CREATE TABLE products (
    id           uuid        PRIMARY KEY,
    -- Порядок добавления для LIFO-удаления. Монотонный счётчик, а не время:
    -- у двух товаров время добавления может совпасть.
    seq          bigint      GENERATED ALWAYS AS IDENTITY,
    reception_id uuid        NOT NULL REFERENCES receptions (id),
    type_id      smallint    NOT NULL REFERENCES product_types (id),
    date_time    timestamptz NOT NULL
);
-- Товары приёмки по порядку: последний товар для удаления и состав приёмки.
-- Заодно покрывает внешний ключ reception_id.
CREATE INDEX products_reception_id_seq_idx ON products (reception_id, seq);

-- +goose Down
DROP TABLE products;
DROP TABLE receptions;
DROP TABLE pvz;
DROP TABLE reception_statuses;
DROP TABLE product_types;
DROP TABLE cities;
