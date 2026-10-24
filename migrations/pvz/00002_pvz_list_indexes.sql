-- Индексы под список ПВЗ (GET /pvz). Замеры EXPLAIN ANALYZE на 10 000 ПВЗ,
-- 300 000 приёмок и 3 млн товаров — в README.

-- +goose Up
-- Страница ПВЗ, новые сверху: база читает из индекса только нужные строки,
-- а не сортирует все ПВЗ ради одной страницы.
CREATE INDEX pvz_registration_date_id_idx ON pvz (registration_date, id);
-- Фильтр по дате приёмки: приёмки за период находятся по индексу, а не
-- перебором всей таблицы. Индекс (pvz_id, date_time) тут не помогает: он
-- начинается с ПВЗ.
CREATE INDEX receptions_date_time_idx ON receptions (date_time);

-- +goose Down
DROP INDEX receptions_date_time_idx;
DROP INDEX pvz_registration_date_id_idx;
