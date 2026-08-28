# Supabase Serverless Setup and Deployment Runbook

This runbook describes the operational steps for configuring Supabase extensions, Vault secrets, Supabase Cron schedules, Supavisor connection limits, and emergency rollback procedures for PaySplit Vercel serverless runtime (Spec 0010).

---

## 1. Supabase Extensions Setup

Execute in Supabase SQL Editor:

```sql
-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "pg_net";
CREATE EXTENSION IF NOT EXISTS "pg_cron";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS "supabase_vault";
```

---

## 2. Vault Secrets Configuration

Store `JOB_TRIGGER_SECRET` securely in Supabase Vault:

```sql
-- Store trigger secret in Vault (replace with your secure 32+ byte random secret)
SELECT vault.create_secret(
    'your-high-entropy-random-job-trigger-secret-at-least-32-bytes',
    'job_trigger_secret',
    'Bearer token for PaySplit internal job dispatcher and drain endpoints'
);

SELECT vault.create_secret(
    'https://your-vercel-domain.vercel.app',
    'paysplit_api_url',
    'Base URL for PaySplit Vercel deployment in sin1'
);
```

---

## 3. Supabase Cron Schedules Setup

Configure minute recovery dispatcher cron:

```sql
-- Create minute cron job calling /internal/jobs/dispatch
SELECT cron.schedule(
    'paysplit-minute-job-dispatch',
    '* * * * *',
    $$
    SELECT net.http_post(
        url := (SELECT decrypted_secret FROM vault.decrypted_secrets WHERE name = 'paysplit_api_url') || '/internal/jobs/dispatch',
        headers := jsonb_build_object(
            'Content-Type', 'application/json',
            'Authorization', 'Bearer ' || (SELECT decrypted_secret FROM vault.decrypted_secrets WHERE name = 'job_trigger_secret'),
            'Cache-Control', 'no-store'
        ),
        body := '{}'::jsonb,
        timeout_milliseconds := 10000
    );
    $$
);
```

---

## 4. Realtime Configuration

1. In Supabase Dashboard -> **Project Settings** -> **Realtime**:
   - Ensure **Broadcast** is enabled.
   - Disable **Postgres Changes** on table `realtime_invalidations` (the table must NOT be in `supabase_realtime` publication).
   - Ensure **Public Channels** are disabled or restricted by RLS on `realtime.messages`.
2. In Supabase Dashboard -> **Authentication** -> **Signing Keys**:
   - Import the asymmetric `ES256` public key corresponding to `SUPABASE_REALTIME_JWT_PRIVATE_KEY`.
   - Record the active `kid` in `SUPABASE_REALTIME_JWT_KID`.

---

## 5. Supavisor Connection Pool Configuration

In Supabase Dashboard -> **Database** -> **Connection Pooling**:
- Pool Mode: **Transaction** (port 6543)
- Backend Pool Size for PaySplit: **15** connections
- Max Client Connections: **200**

---

## 6. Emergency Rollback Commands

If an operational anomaly occurs and job claims / dispatcher need to be paused immediately:

```sql
-- 1. Pause minute cron schedule
SELECT cron.unschedule('paysplit-minute-job-dispatch');

-- 2. Clear active dispatcher lease
UPDATE public.job_wakeup_state 
SET dispatcher_requested = false,
    dispatcher_token = NULL,
    dispatcher_lease_expires_at = NULL
WHERE id = 1;

-- 3. Reset any stuck leased slots to free
UPDATE public.job_drain_slots 
SET state = 'free',
    lease_token = NULL
WHERE state IN ('reserved', 'leased');
```

To re-enable after the issue is resolved:
- Re-run the `cron.schedule` query from Section 3.
- Set `JOB_PROCESSING_ENABLED=true` in Vercel environment variables.
