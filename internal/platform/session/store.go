package session

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"paysplit-backend/internal/modules/auth/domain"
)

// RedisStore triển khai kho phiên trên Redis.
//
// Key được đặt theo HASH của credential chứ không theo credential trần: ai đọc
// được dump Redis, file AOF, hay output MONITOR cũng không đăng nhập lại được.
// Đây đúng là tính chất mà `session_refresh_tokens.token_hash` bên Postgres đang
// có, và không được đánh mất khi chuyển sang Redis.
type RedisStore struct {
	client      goredis.Scripter
	idleTTL     time.Duration
	absoluteTTL time.Duration
}

// NewRedisStore dựng kho phiên. idleTTL là TTL trượt, gia hạn mỗi lần Get;
// absoluteTTL là trần cứng mà không lần gia hạn nào vượt qua được.
func NewRedisStore(client goredis.Scripter, idleTTL, absoluteTTL time.Duration) *RedisStore {
	if client == nil {
		panic("session: redis client must not be nil")
	}
	if idleTTL <= 0 || absoluteTTL <= 0 || idleTTL > absoluteTTL {
		panic("session: TTL values must be positive with idle no greater than absolute")
	}
	return &RedisStore{client: client, idleTTL: idleTTL, absoluteTTL: absoluteTTL}
}

// NewCredential sinh credential mới và hash tương ứng.
//
// Dùng lại domain.NewOpaqueToken — cùng nguồn ngẫu nhiên 32 byte và cùng cách băm
// mà refresh token đang dùng, nên độ mạnh không đổi so với hệ thống cũ. Cố tình
// KHÔNG dùng uuidv7: nó chứa timestamp và chỉ ~74 bit ngẫu nhiên, quá yếu cho một
// credential sống 7-30 ngày.
func NewCredential() (raw string, err error) {
	raw, _, err = domain.NewOpaqueToken()
	return raw, err
}

func credentialKey(raw string) string {
	return "session:" + hex.EncodeToString(domain.HashToken(raw))
}

func credentialHash(raw string) string {
	return hex.EncodeToString(domain.HashToken(raw))
}

func userPointerKey(userID string) string {
	return "user_session:" + userID
}

// Create ghi phiên mới và thu hồi phiên đang có của cùng user, nguyên tử.
func (s *RedisStore) Create(ctx context.Context, raw string, sess Session, now time.Time) error {
	if raw == "" || sess.SID == "" || sess.UserID == "" {
		return errors.New("session: credential, SID and user ID must not be empty")
	}
	hash := credentialHash(raw)
	absoluteExp := now.Add(s.absoluteTTL)
	err := createScript.Run(ctx, s.client,
		[]string{userPointerKey(sess.UserID)},
		hash, sess.SID, sess.UserID, sess.Role,
		absoluteExp.Unix(),
		int64(s.idleTTL.Seconds()),
		int64(s.absoluteTTL.Seconds()),
	).Err()
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Get xác thực credential và gia hạn TTL trượt. Trả ErrNotFound khi credential
// không tồn tại, đã bị thu hồi, hoặc đã vượt trần tuyệt đối.
func (s *RedisStore) Get(ctx context.Context, raw string, now time.Time) (*Session, error) {
	if raw == "" {
		return nil, ErrNotFound
	}
	res, err := getScript.Run(ctx, s.client,
		[]string{credentialKey(raw)},
		now.Unix(), int64(s.idleTTL.Seconds()),
	).Slice()
	if errors.Is(err, goredis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return decodeSession(res)
}

// PeekAndDelete đọc rồi xoá phiên trong một lượt — dùng cho đăng xuất. Bên gọi
// cần SID và user ID trả về để ghi bản ghi audit và phát sự kiện đóng SSE.
// Trả ErrNotFound khi credential không còn, giúp đăng xuất giữ tính idempotent.
func (s *RedisStore) PeekAndDelete(ctx context.Context, raw string) (*Session, error) {
	if raw == "" {
		return nil, ErrNotFound
	}
	res, err := peekAndDeleteScript.Run(ctx, s.client,
		[]string{credentialKey(raw)},
		credentialHash(raw),
	).Slice()
	if errors.Is(err, goredis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("peek and delete session: %w", err)
	}
	return decodeSession(res)
}

// RevokeUserSIDs thu hồi phiên đang sống của user nếu SID của nó nằm trong danh
// sách. Trả về true khi thực sự có phiên bị xoá.
//
// Khớp theo SID chứ không xoá vô điều kiện: hàm này còn được gọi lại từ background
// job vài phút sau sự kiện thu hồi, và nếu trong lúc đó user đã đăng nhập lại thì
// phiên mới mang SID khác và phải được giữ nguyên.
func (s *RedisStore) RevokeUserSIDs(ctx context.Context, userID string, sids []string) (bool, error) {
	if userID == "" || len(sids) == 0 {
		return false, nil
	}
	args := make([]any, 0, len(sids))
	for _, sid := range sids {
		args = append(args, sid)
	}
	revoked, err := revokeUserSIDsScript.Run(ctx, s.client, []string{userPointerKey(userID)}, args...).Int64()
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("revoke user sessions: %w", err)
	}
	return revoked == 1, nil
}

// decodeSession đọc mảng [sid, user_id, role, absolute_exp] mà Lua trả về.
func decodeSession(res []any) (*Session, error) {
	if len(res) != 4 {
		return nil, fmt.Errorf("session: unexpected payload length %d", len(res))
	}
	fields := make([]string, 4)
	for i, v := range res {
		text, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("session: field %d is not a string", i)
		}
		fields[i] = text
	}
	var absoluteExp int64
	if _, err := fmt.Sscanf(fields[3], "%d", &absoluteExp); err != nil {
		return nil, fmt.Errorf("session: parse absolute_exp: %w", err)
	}
	return &Session{
		SID:         fields[0],
		UserID:      fields[1],
		Role:        fields[2],
		AbsoluteExp: time.Unix(absoluteExp, 0),
	}, nil
}

// RevokeUser thu hồi phiên đang sống của user vô điều kiện, không khớp SID.
//
// Dùng ở đường khóa tài khoản, nơi câu hỏi đúng là "user này còn phiên nào
// không" và Redis là nơi duy nhất trả lời được. RevokeUserSIDs vẫn là hàm dành
// cho job retry, vì ở đó việc khớp SID là thứ tránh giết nhầm phiên mới.
//
// Trả về true khi thực sự có phiên bị xoá.
func (s *RedisStore) RevokeUser(ctx context.Context, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	revoked, err := revokeUserScript.Run(ctx, s.client, []string{userPointerKey(userID)}).Int64()
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("revoke user session: %w", err)
	}
	return revoked == 1, nil
}
