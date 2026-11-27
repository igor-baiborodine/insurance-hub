ALTER TABLE public.statement_receipt DROP CONSTRAINT statement_receipt_state_check;
ALTER TABLE public.statement_receipt ADD CONSTRAINT statement_receipt_state_check
    CHECK (state IN ('review', 'posted', 'archived', 'done'))
