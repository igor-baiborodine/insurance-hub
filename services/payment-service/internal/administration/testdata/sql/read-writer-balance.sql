-- Inclusive balance of account $1 on date $2: incoming payments credit, other entries debit.
SELECT coalesce(sum(CASE e.kind WHEN 'in_payment' THEN e.amount ELSE -e.amount END), 0)::text
FROM public.accounting_entry e
JOIN public.policy_account a ON a.id = e.policy_account_id
WHERE a.policy_account_number = $1 AND e.effective_date <= $2::text::date
