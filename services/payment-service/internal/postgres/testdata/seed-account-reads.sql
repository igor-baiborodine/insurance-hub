WITH account_a AS (
    INSERT INTO public.policy_account (
        policy_number, policy_account_number, registered_premium, registered_cover_from,
        created, updated
    ) VALUES (
        'SYN-POLICY-B', 'SYN-ACCOUNT-A', 20.00, DATE '2026-01-01',
        TIMESTAMP '2025-12-20 10:00:00', TIMESTAMP '2026-01-02 11:00:00'
    )
    RETURNING id
), account_b AS (
    INSERT INTO public.policy_account (
        policy_number, policy_account_number, registered_premium, registered_cover_from,
        created, updated
    ) VALUES (
        'SYN-POLICY-OTHER', 'SYN-ACCOUNT-OTHER', 999.00, DATE '2026-01-01',
        TIMESTAMP '2025-12-21 10:00:00', TIMESTAMP '2026-01-03 11:00:00'
    )
    RETURNING id
)
INSERT INTO public.accounting_entry (
    policy_account_id, kind, creation_date, effective_date, amount
)
SELECT id, 'expected_payment', DATE '2025-12-20', DATE '2026-01-01', 10.00 FROM account_a
UNION ALL
SELECT id, 'in_payment', DATE '2026-02-01', DATE '2026-01-07', 15.00 FROM account_a
UNION ALL
SELECT id, 'expected_payment', DATE '2025-12-22', DATE '2026-06-01', 10.00 FROM account_a
UNION ALL
SELECT id, 'out_payment', DATE '2026-06-01', DATE '2026-06-02', 2.50 FROM account_a
UNION ALL
SELECT id, 'in_payment', DATE '2026-01-01', DATE '2026-01-01', 999.00 FROM account_b;
