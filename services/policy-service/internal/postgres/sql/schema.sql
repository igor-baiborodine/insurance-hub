-- Managed Policy schema read by sqlc. It must equal the shape that migration version 1 produces
-- for fresh and adopted databases; the storage suite checks it with the migration inspection.
CREATE TABLE public.offer (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
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
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_login varchar(255),
    number varchar(255)
);
CREATE TABLE public.policy_version (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
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
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code varchar(255),
    price numeric(19, 2),
    policy_version_id bigint REFERENCES public.policy_version (id)
);
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
