-- Expenses: one row per spending event. Amounts are exact NUMERIC with two
-- fractional digits; spent_at is a calendar date; audit timestamps are UTC
-- (timestamptz).
CREATE TABLE expenses (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     BIGINT        NOT NULL CHECK (user_id > 0),
    amount      NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    category    TEXT          NOT NULL CHECK (category IN
                    ('food','transport','housing','entertainment','health','shopping','other')),
    spent_at    DATE          NOT NULL,
    comment     TEXT          NULL CHECK (comment IS NULL OR char_length(comment) BETWEEN 1 AND 500),
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE INDEX expenses_user_spent_idx   ON expenses (user_id, spent_at DESC, id DESC);
CREATE INDEX expenses_user_created_idx ON expenses (user_id, created_at DESC, id DESC);

-- Telegram updates already handled; guarantees a redelivered update never
-- applies its effect twice.
CREATE TABLE telegram_updates (
    update_id    BIGINT      PRIMARY KEY,
    user_id      BIGINT      NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
