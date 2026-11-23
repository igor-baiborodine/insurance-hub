-- Ordinary inserts omit every ID and link dependent rows through returned keys.
WITH new_offer AS (
    INSERT INTO public.offer
        (creation_date, number, policy_from, policy_to, product_code, status, total_price)
    VALUES (DATE '2026-11-24', 'generated-offer', DATE '2026-11-24', DATE '2027-11-23',
        'HSI', 'NEW', 10.00)
    RETURNING id
), new_answer AS (
    INSERT INTO public.offer_answers (offer_id, answer, question_code)
    SELECT id, 'HOUSE', 'TYPE' FROM new_offer RETURNING offer_id
), new_offer_cover AS (
    INSERT INTO public.offer_cover (offer_id, price, cover_code)
    SELECT id, 10.00, 'C1' FROM new_offer RETURNING offer_id
), new_policy AS (
    INSERT INTO public.policy (agent_login, number)
    VALUES ('synthetic.agent', 'generated-policy') RETURNING id
), new_version AS (
    INSERT INTO public.policy_version (account_number, cover_from, cover_to, first_name,
        last_name, pesel, product_code, total_premium_amount, version_number, version_from,
        version_to, policy_id)
    SELECT 'generated-account', DATE '2026-11-24', DATE '2027-11-23', 'Synthetic', 'Holder',
        '00000000000', 'HSI', 10.00, 1, DATE '2026-11-24', DATE '2027-11-23', id
    FROM new_policy RETURNING id, policy_id
), new_cover AS (
    INSERT INTO public.cover (code, price, policy_version_id)
    SELECT 'C1', 10.00, id FROM new_version RETURNING id, policy_version_id
)
SELECT format('%s,%s,%s,%s:%s',
    (SELECT id FROM new_offer), (SELECT id FROM new_policy),
    (SELECT id FROM new_version), (SELECT id FROM new_cover),
    (SELECT offer_id FROM new_answer) = (SELECT id FROM new_offer)
    AND (SELECT offer_id FROM new_offer_cover) = (SELECT id FROM new_offer)
    AND (SELECT policy_id FROM new_version) = (SELECT id FROM new_policy)
    AND (SELECT policy_version_id FROM new_cover) = (SELECT id FROM new_version))
