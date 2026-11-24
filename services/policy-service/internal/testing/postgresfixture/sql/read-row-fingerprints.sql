-- Full-row fingerprints of all six business tables in primary-key order.
SELECT format('offer:%s,offer_answers:%s,offer_cover:%s,policy:%s,policy_version:%s,cover:%s',
    (SELECT md5(string_agg(t::text, '|' ORDER BY id)) FROM public.offer t),
    (SELECT md5(string_agg(t::text, '|' ORDER BY offer_id, question_code))
     FROM public.offer_answers t),
    (SELECT md5(string_agg(t::text, '|' ORDER BY offer_id, cover_code)) FROM public.offer_cover t),
    (SELECT md5(string_agg(t::text, '|' ORDER BY id)) FROM public.policy t),
    (SELECT md5(string_agg(t::text, '|' ORDER BY id)) FROM public.policy_version t),
    (SELECT md5(string_agg(t::text, '|' ORDER BY id)) FROM public.cover t))
