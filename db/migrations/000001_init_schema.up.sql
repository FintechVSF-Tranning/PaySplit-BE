-- ============================================================================
-- PaySplit — PostgreSQL Schema (DDL)
-- Hệ thống chia tiền thông minh (Group Expense-Splitting Prototype)
-- Kiến trúc: Tinh gọn (No Ledger), Theo dõi hoạt động (Activity Tracking)
--
-- Nguyên tắc thiết kế:
--   * Mọi UUID do tầng ứng dụng sinh (UUID v7) để đảm bảo thứ tự theo thời gian.
--   * Mọi bảng thuộc phạm vi nhóm đều mang group_id và dùng khóa ngoại tổ hợp
--     (id, group_id) để DB tự chặn dữ liệu trỏ chéo nhóm.
--   * Tiền tệ lưu bằng BIGINT (đơn vị VND, không có phần thập phân).
-- ============================================================================

-- Khởi tạo các extension cần thiết
CREATE EXTENSION IF NOT EXISTS citext;     -- Hỗ trợ kiểu dữ liệu text không phân biệt hoa thường (dùng cho email)

-- ---------------------------------------------------------------------------
-- 1. ENUM TYPES (Kiểu dữ liệu liệt kê)
-- ---------------------------------------------------------------------------
DO $$ BEGIN CREATE TYPE account_status   AS ENUM ('pending_verification','active','suspended','locked'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE group_role       AS ENUM ('captain','member'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE member_status    AS ENUM ('active','inactive'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE bill_status      AS ENUM ('draft','finalized'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE ocr_job_status   AS ENUM ('queued','processing','succeeded','failed'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE debt_status      AS ENUM ('awaiting','pending_confirmation','stalled_confirmation','rejected','settled'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE admin_action     AS ENUM ('suspend','lock','reactivate'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE token_type       AS ENUM ('email_verification', 'password_reset'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE user_role        AS ENUM ('user', 'admin'); EXCEPTION WHEN duplicate_object THEN null; END $$;
DO $$ BEGIN CREATE TYPE activity_type    AS ENUM ('created_bill', 'updated_bill', 'deleted_bill', 'finalized_bill', 'submitted_proof', 'confirmed_payment', 'rejected_payment', 'stalled_payment_reminder'); EXCEPTION WHEN duplicate_object THEN null; END $$;

-- ---------------------------------------------------------------------------
-- 2. USERS & AUTH (Người dùng & Xác thực)
-- ---------------------------------------------------------------------------

-- Bảng users: Lưu trữ thông tin cá nhân và cấu hình tài khoản ngân hàng.
CREATE TABLE IF NOT EXISTS users (
    id                          UUID PRIMARY KEY,       -- UUID v7 do ứng dụng sinh
    email                       CITEXT NOT NULL UNIQUE, -- Email đăng nhập (không phân biệt hoa/thường)
    password_hash               TEXT NOT NULL,          -- Mật khẩu đã mã hóa (Bắt buộc vì chỉ dùng đăng nhập thường)
    display_name                TEXT NOT NULL,          -- Tên hiển thị trong nhóm
    avatar_object_key           TEXT,                   -- Đường dẫn lưu ảnh đại diện (trên S3/Object Storage)
    phone_number                TEXT,                   -- Số điện thoại liên hệ
    default_bank_code           TEXT,                   -- Mã ngân hàng NAPAS (VD: VCB, TCB) để tạo VietQR
    default_bank_account_number TEXT,                   -- Số tài khoản ngân hàng mặc định nhận tiền
    default_bank_account_holder TEXT,                   -- Tên chủ tài khoản
    role                        user_role NOT NULL DEFAULT 'user',
    status                      account_status NOT NULL DEFAULT 'pending_verification', -- Trạng thái tài khoản
    email_verified_at           TIMESTAMPTZ,            -- Thời điểm xác thực email
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (display_name <> '')
);

-- Bảng sessions: Quản lý các phiên đăng nhập để hỗ trợ tính năng đăng xuất (Revoke Refresh Token).
CREATE TABLE IF NOT EXISTS sessions (
    id                  UUID PRIMARY KEY,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id           TEXT NOT NULL,          -- ID thiết bị của người dùng
    refresh_token_hash  TEXT NOT NULL,          -- Mã hóa của Refresh Token
    issued_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,   -- Hạn sử dụng của token
    revoked_at          TIMESTAMPTZ             -- Thời điểm token bị thu hồi (đăng xuất)
);
CREATE INDEX IF NOT EXISTS idx_sessions_active_by_user ON sessions(user_id) WHERE revoked_at IS NULL;

-- Bảng user_tokens: Gộp chung các loại token xác thực dùng 1 lần (quên mật khẩu, xác minh email)
CREATE TABLE IF NOT EXISTS user_tokens (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        token_type NOT NULL,    -- Phân loại: 'email_verification' hoặc 'password_reset'
    token_hash  TEXT NOT NULL UNIQUE,   -- Chuỗi mã hóa của token
    expires_at  TIMESTAMPTZ NOT NULL,   -- Hạn sử dụng của token
    used_at     TIMESTAMPTZ,            -- Đánh dấu thời điểm đã sử dụng (NULL = chưa dùng)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Tra cứu token còn hiệu lực của 1 user (dùng khi resend email xác thực / reset mật khẩu)
CREATE INDEX IF NOT EXISTS idx_user_tokens_pending ON user_tokens(user_id, type) WHERE used_at IS NULL;

-- ---------------------------------------------------------------------------
-- 3. GROUPS (Quản lý Nhóm chi tiêu)
-- ---------------------------------------------------------------------------

-- Bảng groups: Lưu trữ thông tin cơ bản của nhóm chi tiêu.
CREATE TABLE IF NOT EXISTS groups (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,                          -- Tên nhóm (VD: Du lịch Đà Lạt)
    currency    TEXT NOT NULL DEFAULT 'VND',            -- Tiền tệ sử dụng
    created_by  UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (name <> '')
);

-- Bảng group_members (Quan trọng):
-- Đây là bảng "Mỏ neo" (Anchor). Các bảng hóa đơn (bills), nợ (debts) và thanh toán
-- (payments) sẽ trỏ về bảng này thay vì users.
-- Giúp lưu lại lịch sử đóng góp kể cả khi thành viên rời nhóm (status = inactive).
--
-- LƯU Ý CHO BACKEND: UNIQUE (group_id, user_id) khiến một người đã rời nhóm KHÔNG THỂ
-- được INSERT lại. Luồng "tham gia lại nhóm" phải là UPDATE dòng cũ:
--   UPDATE group_members SET status='active', left_at=NULL WHERE group_id=? AND user_id=?
-- Cách này cố ý giữ nguyên member_id cũ để lịch sử hóa đơn và công nợ không bị đứt gãy.
CREATE TABLE IF NOT EXISTS group_members (
    id          UUID PRIMARY KEY,
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id),
    role        group_role NOT NULL DEFAULT 'member',   -- Vai trò (captain hoặc member)
    status      member_status NOT NULL DEFAULT 'active',-- active: Đang trong nhóm, inactive: Đã rời nhóm
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    left_at     TIMESTAMPTZ,
    UNIQUE (group_id, user_id),
    UNIQUE (id, group_id),                              -- Đích cho các khóa ngoại tổ hợp
    CHECK ((status = 'inactive') = (left_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS idx_group_members_group ON group_members(group_id);
CREATE INDEX IF NOT EXISTS idx_group_members_user  ON group_members(user_id);
-- Mỗi nhóm chỉ có đúng 1 captain đang hoạt động
CREATE UNIQUE INDEX IF NOT EXISTS uq_group_members_active_captain
    ON group_members(group_id) WHERE role = 'captain' AND status = 'active';

-- Bảng group_invites: Quản lý các link/mã mời để người khác tham gia nhóm.
CREATE TABLE IF NOT EXISTS group_invites (
    id          UUID PRIMARY KEY,
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    code        TEXT NOT NULL UNIQUE,                   -- Mã mời (chuỗi ngẫu nhiên)
    created_by  UUID NOT NULL,                          -- Captain đã tạo mã mời
    expires_at  TIMESTAMPTZ NOT NULL,                   -- Hạn sử dụng của mã mời
    max_uses    INT,                                    -- Số lần sử dụng tối đa (NULL = không giới hạn)
    use_count   INT NOT NULL DEFAULT 0,                 -- Số lần đã sử dụng
    revoked_at  TIMESTAMPTZ,                            -- Thời điểm mã mời bị vô hiệu hóa
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (use_count >= 0 AND (max_uses IS NULL OR use_count <= max_uses)),
    FOREIGN KEY (created_by, group_id) REFERENCES group_members(id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_group_invites_active ON group_invites(group_id) WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- 4. BILLS (Quản lý Hóa đơn & Phân bổ Tinh gọn)
-- ---------------------------------------------------------------------------

-- Bảng bills: Thông tin chung của một hóa đơn.
CREATE TABLE IF NOT EXISTS bills (
    id                  UUID PRIMARY KEY,
    group_id            UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    creditor_member_id  UUID NOT NULL,                              -- Người đã ứng tiền trả hóa đơn này
    status              bill_status NOT NULL DEFAULT 'draft',       -- 'draft' (đang nháp) hoặc 'finalized' (đã chốt)
    merchant_name       TEXT,                                       -- Tên cửa hàng/quán ăn
    bill_date           DATE,                                       -- Ngày trên hóa đơn
    image_object_key    TEXT,                                       -- File ảnh hóa đơn chụp lại
    subtotal            BIGINT NOT NULL DEFAULT 0,                  -- Tổng tiền hàng (trước thuế phí)
    service_charge      BIGINT NOT NULL DEFAULT 0,                  -- Phí dịch vụ
    vat                 BIGINT NOT NULL DEFAULT 0,                  -- Thuế VAT
    discount            BIGINT NOT NULL DEFAULT 0,                  -- Tiền giảm giá
    total               BIGINT NOT NULL DEFAULT 0,                  -- Tổng tiền cuối cùng
    mismatch_warning    BOOLEAN NOT NULL DEFAULT false,             -- Cảnh báo nếu OCR quét tổng tiền không khớp chi tiết
    version             INT NOT NULL DEFAULT 1,                     -- Dùng cho Optimistic Locking (chống sửa đồng thời)
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    finalized_at        TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (subtotal >= 0 AND service_charge >= 0 AND vat >= 0 AND discount >= 0 AND total >= 0),
    CHECK ((status = 'finalized') = (finalized_at IS NOT NULL)),
    UNIQUE (id, group_id),
    FOREIGN KEY (creditor_member_id, group_id) REFERENCES group_members(id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_bills_group_status ON bills(group_id, status);

-- Bảng bill_items: Danh sách các món ăn/dịch vụ trong hóa đơn.
-- Ghi chú: KHÔNG ràng buộc cứng line_total = quantity * unit_price, vì hóa đơn thật
-- thường có giảm giá theo từng món. Sai lệch giữa chi tiết và tổng được đánh dấu
-- bằng bills.mismatch_warning để người dùng tự đối chiếu (theo FR 4.1.12).
CREATE TABLE IF NOT EXISTS bill_items (
    id          UUID PRIMARY KEY,
    bill_id     UUID NOT NULL,
    group_id    UUID NOT NULL,
    name        TEXT NOT NULL,                          -- Tên món
    quantity    NUMERIC(10,2) NOT NULL DEFAULT 1,       -- Số lượng (có thể là số thập phân nếu chia kg)
    unit_price  BIGINT NOT NULL,                        -- Đơn giá
    line_total  BIGINT NOT NULL,                        -- Tổng tiền của món
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (quantity > 0 AND unit_price >= 0 AND line_total >= 0),
    UNIQUE (id, group_id),
    FOREIGN KEY (bill_id, group_id) REFERENCES bills(id, group_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_bill_items_bill ON bill_items(bill_id);

-- Bảng bill_item_assignments: Ghi nhận thành viên nào gánh món nào.
-- Đây là nguồn truy vết chi tiết ở mức từng món (thay cho bảng debt_sources đã lược bỏ).
CREATE TABLE IF NOT EXISTS bill_item_assignments (
    id            UUID PRIMARY KEY,
    bill_item_id  UUID NOT NULL,
    group_id      UUID NOT NULL,
    member_id     UUID NOT NULL,
    weight        NUMERIC(10,4) NOT NULL DEFAULT 1,     -- Tỉ trọng gánh món (mặc định chia đều)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (weight > 0),
    FOREIGN KEY (bill_item_id, group_id) REFERENCES bill_items(id, group_id) ON DELETE CASCADE,
    FOREIGN KEY (member_id, group_id) REFERENCES group_members(id, group_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_bill_item_assignment_member ON bill_item_assignments(bill_item_id, member_id);
CREATE INDEX IF NOT EXISTS idx_assignments_member ON bill_item_assignments(member_id);

-- ---------------------------------------------------------------------------
-- 5. GROUP ACTIVITIES (Nhật ký nhóm - Thay thế Sổ cái)
-- ---------------------------------------------------------------------------

-- Bảng group_activities: Lưu mọi biến động (Thêm/Sửa/Xóa hóa đơn, Trả nợ) để đối chiếu
CREATE TABLE IF NOT EXISTS group_activities (
    id              UUID PRIMARY KEY,
    group_id        UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    actor_member_id UUID NOT NULL,                              -- Người thực hiện (phải thuộc đúng nhóm này)
    action_type     activity_type NOT NULL,
    description     TEXT NOT NULL,                              -- Ví dụ: "Nam đã chốt hóa đơn Ăn trưa"
    metadata        JSONB,                                      -- Dữ liệu phụ: {"bill_id": "...", "total": 300000}
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (actor_member_id, group_id) REFERENCES group_members(id, group_id)
);
-- Truy vấn chính: dòng thời gian của 1 nhóm, mới nhất trước
CREATE INDEX IF NOT EXISTS idx_group_activities_timeline ON group_activities(group_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- 6. OCR (Tiến trình trích xuất hình ảnh)
-- ---------------------------------------------------------------------------

-- Bảng ocr_jobs: Theo dõi trạng thái công việc gửi ảnh cho AI đọc.
CREATE TABLE IF NOT EXISTS ocr_jobs (
    id             UUID PRIMARY KEY,
    bill_id        UUID NOT NULL REFERENCES bills(id) ON DELETE CASCADE,
    status         ocr_job_status NOT NULL DEFAULT 'queued',
    provider       TEXT NOT NULL DEFAULT 'gemini-flash',
    attempts       INT NOT NULL DEFAULT 0,
    raw_response   JSONB,                               -- Kết quả gốc trả về từ AI
    error_message  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_ocr_jobs_bill ON ocr_jobs(bill_id);
CREATE INDEX IF NOT EXISTS idx_ocr_jobs_pending ON ocr_jobs(status) WHERE status IN ('queued','processing');

-- ---------------------------------------------------------------------------
-- 7. DEBTS & PAYMENTS (Công nợ theo hóa đơn & Thanh toán gộp)
-- ---------------------------------------------------------------------------
--
-- MÔ HÌNH GỘP NỢ (quan trọng — đây là điểm khác biệt cốt lõi của PaySplit):
--
--   Tầng 1 — debts: ĐƠN VỊ KẾ TOÁN.
--     Mỗi dòng = "trong hóa đơn X, A nợ B bao nhiêu tiền".
--     Nhiều món trong cùng 1 hóa đơn mà A gánh cho B đã được CỘNG GỘP thành 1 dòng
--     duy nhất ngay tại bước Finalize (đảm bảo bởi uq_debts_bill_pair).
--     Chi tiết từng món vẫn truy ngược được qua bill_item_assignments.
--
--   Tầng 2 — payments: ĐƠN VỊ CHUYỂN KHOẢN.
--     Mỗi dòng = "một lần A chuyển khoản cho B" = 1 mã QR = 1 reference_code
--                = 1 ảnh bằng chứng.
--     Một payment gom N dòng debts (N ≥ 1) của cùng cặp (A → B), có thể trải trên
--     nhiều hóa đơn khác nhau. Đây chính là cơ chế "1 QR trả cho nhiều bill".
--
--   Vòng đời: debts.status = awaiting  (chưa có payment)
--             → tạo payment (QR)  → pending_confirmation
--             → creditor xác nhận → settled
--             → creditor từ chối  → rejected → quay lại awaiting để tạo QR mới
--
-- ---------------------------------------------------------------------------

-- Bảng payments: Đại diện cho MỘT LẦN CHUYỂN KHOẢN (1 mã QR + 1 bằng chứng).
CREATE TABLE IF NOT EXISTS payments (
    id                  UUID PRIMARY KEY,
    group_id            UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    debtor_member_id    UUID NOT NULL,                          -- Người chuyển tiền
    creditor_member_id  UUID NOT NULL,                          -- Người nhận tiền
    amount              BIGINT NOT NULL CHECK (amount > 0),     -- Ảnh chụp tổng tiền tại lúc tạo QR
                                                                -- = SUM(debts.amount) của các nợ được gộp.
                                                                -- Cố định sau khi tạo vì đã mã hóa vào QR.
    reference_code      TEXT NOT NULL UNIQUE,                   -- Mã nội dung chuyển khoản (duy nhất toàn hệ thống)
    qr_payload          TEXT,                                   -- Chuỗi VietQR (TLV + CRC-16/CCITT-FALSE)
    image_object_key    TEXT,                                   -- Ảnh chụp màn hình chuyển khoản (bằng chứng)
    note                TEXT,                                   -- Ghi chú của người trả
    rejection_reason    TEXT,                                   -- Lý do creditor từ chối
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),     -- Lúc sinh QR
    submitted_at        TIMESTAMPTZ,                            -- Lúc debtor nộp bằng chứng
    confirmed_at        TIMESTAMPTZ,                            -- Lúc creditor xác nhận đã nhận tiền
    rejected_at         TIMESTAMPTZ,                            -- Lúc creditor từ chối
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (debtor_member_id <> creditor_member_id),
    CHECK ((rejected_at IS NOT NULL) = (rejection_reason IS NOT NULL)),
    CHECK (NOT (confirmed_at IS NOT NULL AND rejected_at IS NOT NULL)),  -- Không thể vừa xác nhận vừa từ chối
    UNIQUE (id, group_id),
    FOREIGN KEY (debtor_member_id, group_id)   REFERENCES group_members(id, group_id),
    FOREIGN KEY (creditor_member_id, group_id) REFERENCES group_members(id, group_id)
);
-- Hộp thư "chờ tôi xác nhận" của chủ nợ
CREATE INDEX IF NOT EXISTS idx_payments_creditor_pending
    ON payments(creditor_member_id) WHERE confirmed_at IS NULL AND rejected_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_payments_debtor ON payments(debtor_member_id);

-- Bảng debts: Công nợ chi tiết theo từng hóa đơn.
CREATE TABLE IF NOT EXISTS debts (
    id                  UUID PRIMARY KEY,
    group_id            UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    bill_id             UUID NOT NULL,                          -- Nợ này phát sinh từ hóa đơn nào
    debtor_member_id    UUID NOT NULL,                          -- Người phải trả
    creditor_member_id  UUID NOT NULL,                          -- Người nhận tiền
    amount              BIGINT NOT NULL CHECK (amount > 0),     -- Tổng phần phải trả trong hóa đơn này
    status              debt_status NOT NULL DEFAULT 'awaiting',
    reminder_count      INT NOT NULL DEFAULT 0,                 -- Số lần đã nhắc nhở
    payment_id          UUID,                                   -- Lần chuyển khoản đang gánh khoản nợ này
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at          TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (debtor_member_id <> creditor_member_id),
    -- 'awaiting' = chưa gắn vào lần chuyển khoản nào; mọi trạng thái khác đều phải có payment
    CHECK ((status = 'awaiting') = (payment_id IS NULL)),
    -- Đã tất toán thì bắt buộc có mốc thời gian, và ngược lại
    CHECK ((status = 'settled') = (settled_at IS NOT NULL)),

    UNIQUE (id, group_id),
    -- Chặn Finalize 2 lần sinh nợ trùng; đồng thời là đích cho ON CONFLICT DO UPDATE.
    -- Đây cũng là ràng buộc đảm bảo "nhiều món trong 1 bill được gộp thành 1 dòng nợ".
    UNIQUE (bill_id, debtor_member_id, creditor_member_id),

    FOREIGN KEY (bill_id, group_id)            REFERENCES bills(id, group_id) ON DELETE CASCADE,
    FOREIGN KEY (debtor_member_id, group_id)   REFERENCES group_members(id, group_id),
    FOREIGN KEY (creditor_member_id, group_id) REFERENCES group_members(id, group_id),
    -- Khóa ngoại tổ hợp: nợ chỉ được gộp vào payment CÙNG NHÓM
    FOREIGN KEY (payment_id, group_id)         REFERENCES payments(id, group_id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_debts_group ON debts(group_id);
-- Hai truy vấn nóng nhất: "tôi còn nợ ai" và "ai còn nợ tôi"
CREATE INDEX IF NOT EXISTS idx_debts_debtor_unsettled   ON debts(debtor_member_id)   WHERE status <> 'settled';
CREATE INDEX IF NOT EXISTS idx_debts_creditor_unsettled ON debts(creditor_member_id) WHERE status <> 'settled';
CREATE INDEX IF NOT EXISTS idx_debts_payment ON debts(payment_id);

-- ---------------------------------------------------------------------------
-- 8. NOTIFICATIONS & ADMIN (Thông báo & Quản trị)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS notifications (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    payload     JSONB,
    read_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications(user_id) WHERE read_at IS NULL;

CREATE TABLE IF NOT EXISTS admin_audit_logs (
    id              UUID PRIMARY KEY,
    admin_id        UUID NOT NULL REFERENCES users(id),
    target_user_id  UUID NOT NULL REFERENCES users(id),
    action          admin_action NOT NULL,
    reason          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (reason <> '')
);
CREATE INDEX IF NOT EXISTS idx_admin_audit_target ON admin_audit_logs(target_user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- 9. AUTOMATIC TIMESTAMPS (Tự động cập nhật updated_at)
-- ---------------------------------------------------------------------------

-- Dùng chung cho mọi bảng có cột updated_at.
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['users','bills','bill_items','ocr_jobs','debts','payments'] LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS trg_%1$s_set_updated_at ON %1$s', t);
        EXECUTE format('CREATE TRIGGER trg_%1$s_set_updated_at BEFORE UPDATE ON %1$s
                        FOR EACH ROW EXECUTE FUNCTION set_updated_at()', t);
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 10. HELPER VIEW (Số dư của nhóm — tính khi đọc, không lưu sổ cái)
-- ---------------------------------------------------------------------------

-- Số dư ròng của từng thành viên: dương = được nhận về, âm = còn phải trả.
-- Dùng cho màn hình tổng quan nhóm và cho điều kiện "net balance = 0" khi rời nhóm (FR 4.1.10).
CREATE OR REPLACE VIEW v_member_balances AS
SELECT m.group_id,
       m.id AS member_id,
       COALESCE(cr.total, 0) - COALESCE(dr.total, 0) AS net_balance
FROM group_members m
LEFT JOIN (SELECT creditor_member_id AS mid, SUM(amount) AS total
           FROM debts WHERE status <> 'settled' GROUP BY 1) cr ON cr.mid = m.id
LEFT JOIN (SELECT debtor_member_id AS mid, SUM(amount) AS total
           FROM debts WHERE status <> 'settled' GROUP BY 1) dr ON dr.mid = m.id;
