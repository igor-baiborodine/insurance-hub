-- Row counts of the three Payment business tables.
SELECT format('accounts=%s;receipts=%s;entries=%s',
    (SELECT count(*) FROM public.policy_account),
    (SELECT count(*) FROM public.statement_receipt),
    (SELECT count(*) FROM public.accounting_entry))
