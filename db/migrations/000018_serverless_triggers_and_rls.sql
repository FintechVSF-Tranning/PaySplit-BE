-- +goose Up
-- Migration 000018: Serverless triggers with fail-open safety and Realtime authorization functions.

-- 1. Statement-level trigger cho app_jobs: tăng requested_generation và set dispatcher_requested
CREATE OR REPLACE FUNCTION fn_app_jobs_statement_wakeup()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
    UPDATE public.job_wakeup_state
    SET requested_generation = requested_generation + 1,
        dispatcher_requested = true,
        updated_at = now()
    WHERE id = 1;
    RETURN NULL;
EXCEPTION WHEN OTHERS THEN
    -- Fail-open: lỗi trigger không bao giờ được làm rollback transaction nghiệp vụ
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_app_jobs_wakeup ON app_jobs;
CREATE TRIGGER trg_app_jobs_wakeup
AFTER INSERT ON app_jobs
FOR EACH STATEMENT
EXECUTE FUNCTION fn_app_jobs_statement_wakeup();

-- 2. Row-level trigger cho realtime_invalidations: phát broadcast metadata theo private topic group:<group_id>
CREATE OR REPLACE FUNCTION fn_realtime_invalidations_broadcast()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    v_payload jsonb;
BEGIN
    v_payload := jsonb_build_object(
        'group_id', NEW.group_id,
        'aggregate_type', NEW.aggregate_type,
        'aggregate_id', NEW.aggregate_id,
        'version', NEW.version,
        'sequence', NEW.sequence,
        'created_at', NEW.created_at
    );
    
    PERFORM pg_notify('realtime:group:' || NEW.group_id::text, v_payload::text);
    RETURN NEW;
EXCEPTION WHEN OTHERS THEN
    -- Fail-open: lỗi realtime broadcast không bao giờ được làm rollback transaction nghiệp vụ
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_realtime_invalidations_broadcast ON realtime_invalidations;
CREATE TRIGGER trg_realtime_invalidations_broadcast
AFTER INSERT ON realtime_invalidations
FOR EACH ROW
EXECUTE FUNCTION fn_realtime_invalidations_broadcast();

-- 3. Hàm kiểm tra quyền tham gia private Realtime channel group:<uuid>
CREATE OR REPLACE FUNCTION fn_authorize_realtime_group_topic(
    p_topic text,
    p_user_id uuid,
    p_session_id uuid
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    v_group_id uuid;
    v_is_active boolean;
    v_session_valid boolean;
BEGIN
    -- Kiểm tra phiên đăng nhập còn hoạt động trong bảng sessions
    SELECT (status = 'active' AND expires_at > now())
    INTO v_session_valid
    FROM public.sessions
    WHERE id = p_session_id AND user_id = p_user_id;

    IF NOT coalesce(v_session_valid, false) THEN
        RETURN false;
    END IF;

    -- Kiểm tra định dạng topic 'group:<uuid>'
    IF NOT p_topic ~ '^group:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
        RETURN false;
    END IF;

    v_group_id := substring(p_topic from 7)::uuid;

    -- Kiểm tra thành viên đang active trong nhóm
    SELECT EXISTS (
        SELECT 1 FROM public.group_members
        WHERE group_id = v_group_id
          AND user_id = p_user_id
          AND status = 'active'
    ) INTO v_is_active;

    RETURN coalesce(v_is_active, false);
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END;
$$;

-- +goose Down
DROP FUNCTION IF EXISTS fn_authorize_realtime_group_topic(text, uuid, uuid);
DROP TRIGGER IF EXISTS trg_realtime_invalidations_broadcast ON realtime_invalidations;
DROP FUNCTION IF EXISTS fn_realtime_invalidations_broadcast();
DROP TRIGGER IF EXISTS trg_app_jobs_wakeup ON app_jobs;
DROP FUNCTION IF EXISTS fn_app_jobs_statement_wakeup();
