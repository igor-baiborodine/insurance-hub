SELECT last_value>=1 AND last_value<9223372036854775807
 AND coalesce((SELECT max(id) FROM public.policy_document)<
 (last_value::numeric+CASE WHEN is_called THEN 1 ELSE 0 END),true)
 FROM public.hibernate_sequence
