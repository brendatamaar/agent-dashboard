-- 0004_phase4_schema.sql

ALTER TABLE schedules ADD COLUMN last_run_at TIMESTAMP;
ALTER TABLE schedules ADD COLUMN next_run_at TIMESTAMP;
