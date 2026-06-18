-- H4: the json_scalars EAV table is removed. 0001 now omits its CREATE, but an
-- existing persistent workspace DuckDB file still carries the table from before
-- this migration, so drop it explicitly. Idempotent: a no-op on a fresh DB where
-- the table was never created.
DROP TABLE IF EXISTS json_scalars;
