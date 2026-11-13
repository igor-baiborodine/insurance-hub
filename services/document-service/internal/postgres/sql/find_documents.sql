SELECT policy_number,
       CASE WHEN bytes IS NULL OR octet_length(bytes) <= $2 THEN bytes END,
       COALESCE(octet_length(bytes), 0),
       bytes IS NULL
FROM public.policy_document
WHERE policy_number = $1
