-- $1 logical source name, $2 SHA-256 digest, $3 state.
INSERT INTO public.statement_receipt (source_name, sha256, state)
VALUES ($1, $2, $3)
RETURNING id
