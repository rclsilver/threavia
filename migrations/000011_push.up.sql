-- Web Push (decided 2026-10-08: the installed web client is the phone client
-- for now, and its notifications are the one external channel).
--
-- push_vapid holds the one key pair that identifies this Core to every push
-- service. It is made on first use and kept: a new one would orphan every
-- subscription, since each browser subscribed against the public half.
--
-- A subscription is one browser on one device, owned by the person who
-- subscribed it and revocable by them. The endpoint is unique: a browser that
-- subscribes again replaces its own row.
CREATE TABLE push_vapid (
    id          SMALLINT PRIMARY KEY DEFAULT 1,
    private_key BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT push_vapid_single CHECK (id = 1)
);

CREATE TABLE push_subscriptions (
    id           UUID PRIMARY KEY,
    owner_id     TEXT NOT NULL,
    endpoint     TEXT NOT NULL UNIQUE,
    p256dh       BYTEA NOT NULL,
    auth         BYTEA NOT NULL,
    subject      TEXT NOT NULL,
    client_id    TEXT NOT NULL DEFAULT '',
    label        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    last_error   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX push_subscriptions_owner ON push_subscriptions (owner_id);
