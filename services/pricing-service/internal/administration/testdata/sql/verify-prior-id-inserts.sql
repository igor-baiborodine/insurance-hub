SET ROLE go_pricing_seeder;
DO $$
DECLARE
    rejected_column text;
BEGIN
    BEGIN
        INSERT INTO public.tariff (code) VALUES ('PROBE');
        RAISE EXCEPTION 'expected prior tariff insert to require an explicit ID';
    EXCEPTION WHEN not_null_violation THEN
        GET STACKED DIAGNOSTICS rejected_column = COLUMN_NAME;
        IF rejected_column <> 'id' THEN RAISE; END IF;
    END;
    BEGIN
        INSERT INTO public.discount_markup_rule (type, tariff_id, position)
        SELECT 'perc_markup', id, 99 FROM public.tariff WHERE code = 'TRI';
        RAISE EXCEPTION 'expected prior markup insert to require an explicit ID';
    EXCEPTION WHEN not_null_violation THEN
        GET STACKED DIAGNOSTICS rejected_column = COLUMN_NAME;
        IF rejected_column <> 'id' THEN RAISE; END IF;
    END;
END;
$$;
RESET ROLE;
