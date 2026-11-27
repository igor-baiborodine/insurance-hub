-- Whether a replayed registration ($2 premium, $3 coverage start) equals the recorded one.
SELECT registered_premium = $2::text::numeric AND registered_cover_from = $3::text::date
FROM public.policy_account
WHERE policy_number = $1
