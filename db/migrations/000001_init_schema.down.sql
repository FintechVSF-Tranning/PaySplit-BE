DROP VIEW IF EXISTS v_member_balances;

DROP TABLE IF EXISTS admin_audit_logs;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS debts;
DROP TABLE IF EXISTS payments;
DROP TABLE IF EXISTS ocr_jobs;
DROP TABLE IF EXISTS bill_item_assignments;
DROP TABLE IF EXISTS bill_items;
DROP TABLE IF EXISTS bills;
DROP TABLE IF EXISTS group_invites;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;
DROP TABLE IF EXISTS user_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;

DROP FUNCTION IF EXISTS set_updated_at();

DROP TYPE IF EXISTS activity_type;
DROP TYPE IF EXISTS user_role;
DROP TYPE IF EXISTS token_type;
DROP TYPE IF EXISTS admin_action;
DROP TYPE IF EXISTS debt_status;
DROP TYPE IF EXISTS ocr_job_status;
DROP TYPE IF EXISTS bill_status;
DROP TYPE IF EXISTS member_status;
DROP TYPE IF EXISTS group_role;
DROP TYPE IF EXISTS account_status;

DROP EXTENSION IF EXISTS citext;
