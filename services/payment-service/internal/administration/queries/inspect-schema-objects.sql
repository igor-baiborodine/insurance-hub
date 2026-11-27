-- Business table count and Payment migration history presence.
SELECT
    (SELECT count(*) FROM unnest(ARRAY[
        'public.policy_account', 'public.statement_receipt', 'public.accounting_entry'
    ]) AS name WHERE to_regclass(name) IS NOT NULL)::integer,
    to_regclass('public.payment_schema_version') IS NOT NULL
