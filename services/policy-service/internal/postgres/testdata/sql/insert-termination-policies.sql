-- Two synthetic one-version policies for the termination arithmetic: one annual 120.00 cover and
-- two 0.05 covers over a two-day period.
WITH annual AS (
    INSERT INTO public.policy (number, agent_login)
    VALUES ('termination-annual', 'synthetic.agent')
    RETURNING id
), annual_version AS (
    INSERT INTO public.policy_version (policy_id, version_number, product_code, first_name,
        last_name, pesel, account_number, cover_from, cover_to, version_from, version_to,
        total_premium_amount)
    SELECT id, 1, 'HSI', 'Synthetic', 'Holder', '00000000000', 'annual-account',
        DATE '2026-01-01', DATE '2026-12-31', DATE '2026-01-01', DATE '2026-12-31', 120.00
    FROM annual
    RETURNING id
)
INSERT INTO public.cover (policy_version_id, code, price)
SELECT id, 'C1', 120.00 FROM annual_version;

WITH two_day AS (
    INSERT INTO public.policy (number) VALUES ('termination-two-day') RETURNING id
), two_day_version AS (
    INSERT INTO public.policy_version (policy_id, version_number, product_code, first_name,
        last_name, pesel, account_number, cover_from, cover_to, version_from, version_to,
        total_premium_amount)
    SELECT id, 1, 'HSI', 'Synthetic', 'Holder', '00000000000', 'two-day-account',
        DATE '2026-06-30', DATE '2026-07-01', DATE '2026-06-30', DATE '2026-07-01', 0.10
    FROM two_day
    RETURNING id
)
INSERT INTO public.cover (policy_version_id, code, price)
SELECT id, code, 0.05 FROM two_day_version, (VALUES ('C1'), ('C2')) AS codes (code);
