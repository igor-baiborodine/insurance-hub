-- name: ListAccounts :many
SELECT policy_account_number, policy_number, created, updated
FROM public.policy_account;

-- name: InsertPolicyAccount :one
INSERT INTO public.policy_account (
    policy_number, policy_account_number, registered_premium, registered_cover_from,
    created, updated
)
VALUES (
    sqlc.arg(policy_number), sqlc.arg(policy_account_number),
    sqlc.arg(registered_premium)::text::numeric, sqlc.arg(registered_cover_from),
    sqlc.arg(registered_at), sqlc.arg(registered_at)
)
ON CONFLICT (policy_number) DO NOTHING
RETURNING id;

-- name: RegistrationMatches :one
SELECT registered_premium = sqlc.arg(registered_premium)::text::numeric
    AND registered_cover_from = sqlc.arg(registered_cover_from)
FROM public.policy_account
WHERE policy_number = sqlc.arg(policy_number);

-- name: InsertExpectedPayment :exec
INSERT INTO public.accounting_entry (
    policy_account_id, kind, creation_date, effective_date, amount
)
VALUES (
    sqlc.arg(policy_account_id), 'expected_payment', sqlc.arg(creation_date),
    sqlc.arg(effective_date), sqlc.arg(amount)::text::numeric
);

-- name: LockStatementSource :exec
SELECT pg_catalog.pg_advisory_xact_lock(
    pg_catalog.hashtextextended(sqlc.arg(source_name)::text, 487)
);

-- name: ListStatementReceipts :many
SELECT id, sha256, state
FROM public.statement_receipt
WHERE source_name = sqlc.arg(source_name)
ORDER BY id;

-- name: FindPolicyAccountID :one
SELECT id
FROM public.policy_account
WHERE policy_account_number = sqlc.arg(account_number);

-- name: InsertStatementReceipt :one
INSERT INTO public.statement_receipt (source_name, sha256, state)
VALUES (sqlc.arg(source_name), sqlc.arg(sha256), sqlc.arg(state))
RETURNING id, state;

-- name: InsertStatementCredit :exec
INSERT INTO public.accounting_entry (
    policy_account_id, kind, creation_date, effective_date, amount,
    statement_receipt_id
)
VALUES (
    sqlc.arg(policy_account_id), 'in_payment', sqlc.arg(creation_date),
    sqlc.arg(effective_date), sqlc.arg(amount)::text::numeric,
    sqlc.arg(statement_receipt_id)
);

-- name: GetAccountBalance :one
SELECT a.policy_account_number, a.policy_number, a.created, a.updated,
    COALESCE(SUM(CASE e.kind
        WHEN 'in_payment' THEN e.amount
        WHEN 'expected_payment' THEN -e.amount
        WHEN 'out_payment' THEN -e.amount
    END), 0)::numeric(19, 2) AS balance
FROM public.policy_account AS a
LEFT JOIN public.accounting_entry AS e
    ON e.policy_account_id = a.id
    AND e.effective_date <= sqlc.arg(business_date)
WHERE a.policy_account_number = sqlc.arg(account_number)
GROUP BY a.id, a.policy_account_number, a.policy_number, a.created, a.updated;
