SELECT c.relkind='S' AND c.relowner='document'::regrole
 AND s.seqtypid='bigint'::regtype AND s.seqstart=1 AND s.seqincrement=1
 AND s.seqmin=1 AND s.seqmax=9223372036854775807 AND s.seqcache=1 AND NOT s.seqcycle
 AND NOT EXISTS(SELECT FROM pg_depend WHERE classid='pg_class'::regclass
    AND objid=c.oid AND deptype IN ('a','i'))
 FROM pg_class c JOIN pg_sequence s ON s.seqrelid=c.oid
 WHERE c.oid='public.hibernate_sequence'::regclass
