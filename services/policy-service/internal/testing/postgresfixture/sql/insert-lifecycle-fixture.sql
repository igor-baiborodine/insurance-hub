-- Independently constructed lifecycle data in the migrated tables: a still-valid offer, an
-- originally issued version 1 and a version 2 that ends the cover early. IDs are generated.
WITH new_offer AS (
    INSERT INTO public.offer
        (creation_date, number, policy_from, policy_to, product_code, status, total_price)
    VALUES (DATE '2026-11-13', '7e0a1c84-6f43-4c1e-9b7a-2f5d0c3e8a11', DATE '2026-11-13',
        DATE '2027-11-13', 'HSI', 'NEW', 123.45)
    RETURNING id
), new_answers AS (
    INSERT INTO public.offer_answers (offer_id, answer, question_code)
    SELECT id, answer, question_code FROM new_offer,
        (VALUES ('APARTMENT', 'TYPE'), ('0', 'NUM_OF_CLAIM')) AS answer (answer, question_code)
), new_offer_covers AS (
    INSERT INTO public.offer_cover (offer_id, price, cover_code)
    SELECT id, price, cover_code FROM new_offer,
        (VALUES (100.00, 'C1'), (23.45, 'C2')) AS cover (price, cover_code)
), new_policy AS (
    INSERT INTO public.policy (agent_login, number)
    VALUES ('synthetic.agent', 'a9c43c58-0f9e-4f68-8c1d-5b6c1e2f7d22')
    RETURNING id
), new_versions AS (
    INSERT INTO public.policy_version (account_number, cover_from, cover_to, first_name,
        last_name, pesel, product_code, total_premium_amount, version_number, version_from,
        version_to, policy_id)
    SELECT '31f5d9a2-8b7c-4e61-a0d4-6c2b9e7f1a33', DATE '2026-11-13', cover_to, 'Synthetic',
        'Holder', '00000000000', 'HSI', premium, version_number, version_from,
        DATE '2027-11-13', new_policy.id
    FROM new_policy, (VALUES
        (1, 123.45, DATE '2027-11-13', DATE '2026-11-13'),
        (2, 3.71, DATE '2026-11-23', DATE '2026-11-24')
    ) AS version (version_number, premium, cover_to, version_from)
    RETURNING id, version_number
)
INSERT INTO public.cover (code, price, policy_version_id)
SELECT cover.code, cover.price, new_versions.id
FROM new_versions JOIN (VALUES
    (1, 'C1', 100.00), (1, 'C2', 23.45), (2, 'C1', 3.01), (2, 'C2', 0.70)
) AS cover (version_number, code, price) USING (version_number)
