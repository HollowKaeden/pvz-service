-- +goose Up
CREATE TABLE rates (
    product_type text PRIMARY KEY,
    amount_kopecks bigint NOT NULL CHECK (amount_kopecks > 0)
);
INSERT INTO rates VALUES ('электроника', 10000), ('одежда', 5000), ('обувь', 3000);

CREATE TABLE payouts (
    event_id uuid PRIMARY KEY,
    reception_id uuid NOT NULL,
    pvz_id uuid NOT NULL,
    city text NOT NULL,
    amount_kopecks bigint NOT NULL CHECK (amount_kopecks > 0),
    products integer NOT NULL CHECK (products > 0),
    closed_at timestamptz NOT NULL,
    processed_at timestamptz NOT NULL
);
CREATE INDEX payouts_pvz_processed_idx ON payouts (pvz_id, processed_at DESC, event_id DESC);
CREATE TABLE balances (
    pvz_id uuid PRIMARY KEY,
    city text NOT NULL,
    amount_kopecks bigint NOT NULL CHECK (amount_kopecks >= 0),
    updated_at timestamptz NOT NULL
);
-- Долговечное намерение reset: сбой между БД и Kafka завершается при старте.
CREATE TABLE pending_reset (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    offsets jsonb NOT NULL
);

-- +goose Down
DROP TABLE pending_reset, balances, payouts, rates;
