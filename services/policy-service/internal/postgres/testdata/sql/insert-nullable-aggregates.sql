INSERT INTO public.offer (number) VALUES ('nullable-offer');
INSERT INTO public.offer_answers (offer_id, question_code)
SELECT id, 'nullable-answer' FROM public.offer WHERE number = 'nullable-offer';
INSERT INTO public.offer_cover (offer_id, cover_code)
SELECT id, 'nullable-cover' FROM public.offer WHERE number = 'nullable-offer';
INSERT INTO public.offer (number, total_price) VALUES ('zero-offer', 0.00);

INSERT INTO public.policy (number) VALUES ('nullable-policy');
INSERT INTO public.policy_version (policy_id)
SELECT id FROM public.policy WHERE number = 'nullable-policy';
INSERT INTO public.cover (policy_version_id)
SELECT version.id
FROM public.policy_version AS version
JOIN public.policy ON public.policy.id = version.policy_id
WHERE public.policy.number = 'nullable-policy';
