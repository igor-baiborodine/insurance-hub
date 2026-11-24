-- Moves one stored offer's creation date so the business date falls on a chosen offer age.
UPDATE public.offer SET creation_date = $2 WHERE number = $1
