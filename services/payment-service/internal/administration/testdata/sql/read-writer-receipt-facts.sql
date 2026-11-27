-- Receipts, distinct digests and linked credits recorded under one logical source name.
SELECT format('receipts=%s;digests=%s;credits=%s', count(DISTINCT r.id), count(DISTINCT r.sha256),
    count(e.id))
FROM public.statement_receipt r
LEFT JOIN public.accounting_entry e ON e.statement_receipt_id = r.id
WHERE r.source_name = $1
