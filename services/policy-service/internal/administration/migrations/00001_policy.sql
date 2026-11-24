-- +goose Up
-- The six business tables keep the Java column, key and nullability shape. A fresh database
-- creates them here; an inspected Java database keeps its tables and rows.
-- +goose StatementBegin
DO $$
BEGIN
    IF to_regclass('public.offer') IS NULL THEN
        CREATE TABLE public.offer (
            id bigint NOT NULL PRIMARY KEY,
            creation_date date,
            number varchar(255),
            policy_from date,
            policy_to date,
            product_code varchar(255),
            status varchar(255),
            total_price numeric(19, 2)
        );
        CREATE TABLE public.offer_answers (
            offer_id bigint NOT NULL REFERENCES public.offer (id),
            answer varchar(255),
            question_code varchar(255) NOT NULL,
            PRIMARY KEY (offer_id, question_code)
        );
        CREATE TABLE public.offer_cover (
            offer_id bigint NOT NULL REFERENCES public.offer (id),
            price numeric(19, 2),
            cover_code varchar(255) NOT NULL,
            PRIMARY KEY (offer_id, cover_code)
        );
        CREATE TABLE public.policy (
            id bigint NOT NULL PRIMARY KEY,
            agent_login varchar(255),
            number varchar(255)
        );
        CREATE TABLE public.policy_version (
            id bigint NOT NULL PRIMARY KEY,
            account_number varchar(255),
            cover_from date,
            cover_to date,
            first_name varchar(255),
            last_name varchar(255),
            pesel varchar(255),
            product_code varchar(255),
            total_premium_amount numeric(19, 2),
            version_number bigint,
            version_from date,
            version_to date,
            policy_id bigint REFERENCES public.policy (id)
        );
        CREATE TABLE public.cover (
            id bigint NOT NULL PRIMARY KEY,
            code varchar(255),
            price numeric(19, 2),
            policy_version_id bigint REFERENCES public.policy_version (id)
        );
    END IF;
END $$;
-- +goose StatementEnd

-- Block concurrent writers while each table-local identity is created and aligned once.
LOCK TABLE public.offer, public.offer_answers, public.offer_cover,
    public.policy, public.policy_version, public.cover IN ACCESS EXCLUSIVE MODE;

ALTER TABLE public.offer ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
ALTER TABLE public.policy ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
ALTER TABLE public.policy_version ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;
ALTER TABLE public.cover ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY;

-- An empty table keeps its fresh identity at 1. Otherwise the next value follows that
-- table's own maximum. Goose records this version, so a replay never rewinds an allocator.
SELECT setval(pg_get_serial_sequence('public.offer', 'id'), max(id))
FROM public.offer HAVING max(id) IS NOT NULL;
SELECT setval(pg_get_serial_sequence('public.policy', 'id'), max(id))
FROM public.policy HAVING max(id) IS NOT NULL;
SELECT setval(pg_get_serial_sequence('public.policy_version', 'id'), max(id))
FROM public.policy_version HAVING max(id) IS NOT NULL;
SELECT setval(pg_get_serial_sequence('public.cover', 'id'), max(id))
FROM public.cover HAVING max(id) IS NOT NULL;

-- Retire the Java shared allocator only after all four identities are aligned.
DROP SEQUENCE IF EXISTS public.hibernate_sequence;

-- One logical event intent per aggregate change. The identity orders intents per policy.
CREATE TABLE public.policy_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    intent_id uuid NOT NULL UNIQUE,
    policy_id bigint NOT NULL REFERENCES public.policy (id),
    topic varchar(64) NOT NULL CHECK (topic IN ('policy-registered', 'policy-terminated')),
    message_key varchar(255) NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at timestamptz
);
CREATE INDEX policy_outbox_pending_idx
    ON public.policy_outbox (policy_id, id) WHERE published_at IS NULL;
