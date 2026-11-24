INSERT INTO public.policy_outbox (intent_id, policy_id, topic, message_key, payload)
SELECT '33333333-3333-4333-8333-333333333333', id, 'policy-registered', number, '{}'::jsonb
FROM public.policy WHERE number = 'generated-policy'
RETURNING id
