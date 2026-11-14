INSERT INTO public.policy_document (id, bytes, policy_number)
VALUES (nextval('public.hibernate_sequence'), $1, $2)
