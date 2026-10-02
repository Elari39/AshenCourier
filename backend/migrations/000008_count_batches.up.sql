CREATE TABLE click_count_batches (
 batch_id uuid PRIMARY KEY,
 short_code text NOT NULL,
 delta bigint NOT NULL CHECK (delta > 0),
 committed_at timestamptz NOT NULL DEFAULT now()
);
