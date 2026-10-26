-- Пользователи для /register и /login. Роль хранится ссылкой на справочник,
-- как город ПВЗ и статус приёмки. Сам пароль не хранится, только bcrypt-хеш.

-- +goose Up
CREATE TABLE roles (
    id   smallint PRIMARY KEY,
    name text     NOT NULL UNIQUE
);
INSERT INTO roles (id, name) VALUES
    (1, 'employee'),
    (2, 'moderator');

CREATE TABLE users (
    id            uuid     PRIMARY KEY,
    -- Сервис приводит email к нижнему регистру до записи и до поиска, поэтому
    -- хватает обычной уникальности, без lower(). Она же — индекс для входа.
    email         text     NOT NULL,
    password_hash text     NOT NULL,
    role_id       smallint NOT NULL REFERENCES roles (id),
    CONSTRAINT users_email_key UNIQUE (email)
);

-- +goose Down
DROP TABLE users;
DROP TABLE roles;
