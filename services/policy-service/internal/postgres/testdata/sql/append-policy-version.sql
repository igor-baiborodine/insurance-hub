WITH selected_policy AS (
    SELECT id FROM public.policy
    WHERE number = 'a9c43c58-0f9e-4f68-8c1d-5b6c1e2f7d22'
), appended_version AS (
    INSERT INTO public.policy_version (policy_id, version_number, total_premium_amount)
    SELECT id, 3, 9.99 FROM selected_policy
    RETURNING id
)
INSERT INTO public.cover (policy_version_id, code, price)
SELECT id, 'CONCURRENT', 9.99 FROM appended_version;
