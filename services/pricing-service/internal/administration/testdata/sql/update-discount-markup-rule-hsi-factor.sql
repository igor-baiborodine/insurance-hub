UPDATE public.discount_markup_rule
SET param_value = 1.30
WHERE tariff_id = (SELECT id FROM public.tariff WHERE code = 'HSI')
  AND type = 'perc_markup'
  AND apply_if_formula = 'NUM_OF_CLAIM > 1 '
