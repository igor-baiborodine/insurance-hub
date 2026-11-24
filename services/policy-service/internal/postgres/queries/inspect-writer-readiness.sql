-- $1 is the writer role. Bounded reads prove the session identity and access to every table the
-- service requires; a missing table or privilege fails the query.
SELECT current_user = $1 AND session_user = $1 AND current_database() = 'policy'
    AND (SELECT count(*) FROM (SELECT 1 FROM public.offer LIMIT 1) AS t) >= 0
    AND (SELECT count(*) FROM (SELECT 1 FROM public.offer_answers LIMIT 1) AS t) >= 0
    AND (SELECT count(*) FROM (SELECT 1 FROM public.offer_cover LIMIT 1) AS t) >= 0
    AND (SELECT count(*) FROM (SELECT 1 FROM public.policy LIMIT 1) AS t) >= 0
    AND (SELECT count(*) FROM (SELECT 1 FROM public.policy_version LIMIT 1) AS t) >= 0
    AND (SELECT count(*) FROM (SELECT 1 FROM public.cover LIMIT 1) AS t) >= 0
    AND (SELECT count(*) FROM (SELECT 1 FROM public.policy_outbox LIMIT 1) AS t) >= 0
