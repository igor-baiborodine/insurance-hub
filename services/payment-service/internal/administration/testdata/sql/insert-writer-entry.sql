-- $1 account ID, $2 kind, $3 statement receipt ID or null.
INSERT INTO public.accounting_entry (policy_account_id, kind, creation_date, effective_date, amount,
    statement_receipt_id)
VALUES ($1, $2, '2026-08-02', '2026-08-01', 10.21, $3)
