-- +goose Up
CREATE TABLE outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id uuid NOT NULL UNIQUE,
    pvz_id uuid NOT NULL,
    body jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz
);

-- Опрос пустой очереди не читает уже отправленные события.
CREATE INDEX outbox_pending_idx ON outbox (id) WHERE sent_at IS NULL;
CREATE INDEX outbox_sent_idx ON outbox (sent_at, id) WHERE sent_at IS NOT NULL;

-- +goose Down
DROP TABLE outbox;
