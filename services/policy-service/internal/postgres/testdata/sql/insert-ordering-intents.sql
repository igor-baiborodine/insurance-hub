-- Pending intents in commit order: policy A registration, policy A termination, then policy B
-- registration. A second publisher must skip A's termination while A's registration is locked.
WITH policy_a AS (
    INSERT INTO public.policy (number) VALUES ('ordering-a') RETURNING id
)
INSERT INTO public.policy_outbox (intent_id, policy_id, topic, message_key, payload)
SELECT '44444444-4444-4444-8444-444444444441'::uuid, id, 'policy-registered', 'ordering-a',
    '{"policy":{"number":"ordering-a"}}'::jsonb
FROM policy_a;

INSERT INTO public.policy_outbox (intent_id, policy_id, topic, message_key, payload)
SELECT '44444444-4444-4444-8444-444444444442'::uuid, id, 'policy-terminated', 'ordering-a',
    '{"policy":{"number":"ordering-a"}}'::jsonb
FROM public.policy
WHERE number = 'ordering-a';

WITH policy_b AS (
    INSERT INTO public.policy (number) VALUES ('ordering-b') RETURNING id
)
INSERT INTO public.policy_outbox (intent_id, policy_id, topic, message_key, payload)
SELECT '44444444-4444-4444-8444-444444444443'::uuid, id, 'policy-registered', 'ordering-b',
    '{"policy":{"number":"ordering-b"}}'::jsonb
FROM policy_b;
