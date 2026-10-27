-- 0003_phase3_schema.sql

ALTER TABLE agents ADD COLUMN failure_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN auto_approve INTEGER NOT NULL DEFAULT 0;
