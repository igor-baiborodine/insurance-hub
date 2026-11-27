-- One registration: the account with its registered premium and coverage start, and the expected
-- premium debit effective on coverage start. $1 policy, $2 account, $3 premium, $4 coverage start.
WITH account AS (
    INSERT INTO public.policy_account (policy_number, policy_account_number, registered_premium,
        registered_cover_from, created, updated)
    VALUES ($1, $2, $3::text::numeric, $4::text::date, '2026-01-01 12:00:00',
        '2026-01-01 12:00:00')
    RETURNING id, registered_premium, registered_cover_from
)
INSERT INTO public.accounting_entry (policy_account_id, kind, creation_date, effective_date, amount)
SELECT id, 'expected_payment', registered_cover_from, registered_cover_from, registered_premium
FROM account
RETURNING policy_account_id
