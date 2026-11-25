-- +goose Up
-- Persist only the W3C trace fields needed to correlate an outbox intent after a retry or process
-- restart. Existing pending intents remain valid with both fields NULL.
ALTER TABLE public.policy_outbox
    ADD COLUMN trace_parent varchar(55),
    ADD COLUMN trace_state varchar(512);
