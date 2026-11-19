-- Durable deduplication for Safe proposal/signature approval notifications.
-- The activation timestamp prevents the first reconciliation after rollout
-- from replaying signatures that predate this feature.
CREATE TABLE safe_approval_notification_config (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    activated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO safe_approval_notification_config (singleton) VALUES (TRUE);

CREATE TABLE safe_approval_milestones (
    chain_id BIGINT NOT NULL,
    safe_tx_hash TEXT NOT NULL CHECK (safe_tx_hash = lower(safe_tx_hash)),
    signer_address TEXT NOT NULL CHECK (signer_address = lower(signer_address)),
    event_id UUID NOT NULL UNIQUE,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (chain_id, safe_tx_hash, signer_address)
);

CREATE INDEX safe_approval_milestones_recorded_idx
    ON safe_approval_milestones (recorded_at DESC);
