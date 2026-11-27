UPDATE public.statement_receipt SET state = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1
