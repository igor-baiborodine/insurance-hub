-- name: ListAccounts :many
SELECT policy_account_number, policy_number, created, updated
FROM public.policy_account;

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
