CREATE TABLE human_reviews (
    session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    version integer NOT NULL DEFAULT 0,
    document jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
