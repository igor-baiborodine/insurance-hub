-- One account per registered policy. Timestamps are UTC wall-clock values. The registration
-- premium and coverage start are both present for an account created from a policy registration,
-- so a replay can be compared with the original, and both absent otherwise.
CREATE TABLE public.policy_account (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    policy_number varchar(255) NOT NULL UNIQUE,
    policy_account_number varchar(255) NOT NULL UNIQUE,
    registered_premium numeric(19, 2),
    registered_cover_from date,
    created timestamp NOT NULL,
    updated timestamp NOT NULL,
    CHECK ((registered_premium IS NULL) = (registered_cover_from IS NULL))
);

-- One accepted bank statement file, identified by its logical name and content digest. A changed
-- file under the same name is a separate receipt held for review.
CREATE TABLE public.statement_receipt (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_name varchar(255) NOT NULL,
    sha256 varchar(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    state varchar(16) NOT NULL CHECK (state IN ('review', 'posted', 'archived')),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (source_name, sha256)
);

-- Dated debits and credits. Expected and outgoing payments are debits; incoming payments are
-- credits. Identical credits stay distinct rows; a statement credit references its receipt.
CREATE TABLE public.accounting_entry (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    policy_account_id bigint NOT NULL REFERENCES public.policy_account (id),
    kind varchar(16) NOT NULL
        CHECK (kind IN ('expected_payment', 'in_payment', 'out_payment')),
    creation_date date NOT NULL,
    effective_date date NOT NULL,
    amount numeric(19, 2) NOT NULL,
    statement_receipt_id bigint REFERENCES public.statement_receipt (id)
);
CREATE INDEX accounting_entry_account_idx
    ON public.accounting_entry (policy_account_id, effective_date);

-- Applied schema versions; written only by the migrator in the same transaction as the schema.
CREATE TABLE public.payment_schema_version (
    version_id bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
