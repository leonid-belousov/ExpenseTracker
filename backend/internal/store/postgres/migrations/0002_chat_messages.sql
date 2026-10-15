-- Short-term conversation memory of the Telegram bot: recent user messages
-- and bot replies per member. Rows older than the retention window are
-- pruned by the application on every write.
CREATE TABLE chat_messages (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     BIGINT      NOT NULL CHECK (user_id > 0),
    role        TEXT        NOT NULL CHECK (role IN ('user', 'assistant')),
    content     TEXT        NOT NULL CHECK (char_length(content) BETWEEN 1 AND 4000),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX chat_messages_user_idx ON chat_messages (user_id, created_at DESC, id DESC);
