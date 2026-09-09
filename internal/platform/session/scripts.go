package session

import goredis "github.com/redis/go-redis/v9"

// Bốn thao tác dưới đây đều phải nguyên tử. Redis không có transaction kiểu
// Postgres, và mỗi thao tác đụng tới hai key (bản ghi phiên + chỉ mục ngược
// user_session), nên tách thành nhiều lệnh rời sẽ để lộ cửa sổ race — ví dụ hai
// lần đăng nhập đồng thời có thể để lại một bản ghi phiên mồ côi không key nào
// trỏ tới, và thiết bị cũ tiếp tục đăng nhập được cho tới khi TTL hết.
//
// Trả về của getScript / peekAndDeleteScript luôn theo đúng thứ tự
// [sid, user_id, role, absolute_exp] để phía Go đọc bằng chỉ số, thay vì HGETALL
// trả mảng phẳng phải tự ghép cặp.

// createScript tạo phiên mới và giết phiên cũ của cùng user trong một lượt.
//
// KEYS[1] = user_session:<user_id>
// ARGV    = hash, sid, user_id, role, absolute_exp(unix), idleTTL(giây), absTTL(giây)
var createScript = goredis.NewScript(`
local newKey = 'session:' .. ARGV[1]
local old = redis.call('GET', KEYS[1])
-- Chỉ ghi đè con trỏ là chưa đủ: bản ghi phiên cũ giữ TTL riêng của nó, không
-- còn chỉ mục nào trỏ tới, nên sẽ sống tiếp và thiết bị cũ vẫn đăng nhập được.
if old and old ~= ARGV[1] then
  redis.call('DEL', 'session:' .. old)
end
redis.call('HSET', newKey, 'sid', ARGV[2], 'user_id', ARGV[3], 'role', ARGV[4], 'absolute_exp', ARGV[5])
redis.call('EXPIRE', newKey, ARGV[6])
-- Con trỏ mang TTL tuyệt đối, KHÔNG phải TTL trượt: bản ghi phiên được gia hạn
-- mỗi request nên có thể sống lâu hơn 7 ngày, và nếu con trỏ hết hạn trước thì
-- mất luôn khả năng thu hồi theo user (đổi mật khẩu, admin khoá tài khoản).
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[7])
return 1
`)

// getScript xác thực credential và gia hạn TTL trượt trong MỘT round-trip.
// HGETALL rồi EXPIRE rời nhau sẽ tốn hai lượt đi về cho mỗi request có auth.
//
// KEYS[1] = session:<hash>
// ARGV[1] = now (unix), ARGV[2] = idle TTL (giây)
var getScript = goredis.NewScript(`
local v = redis.call('HMGET', KEYS[1], 'sid', 'user_id', 'role', 'absolute_exp')
if not v[1] then return nil end
local absExp = tonumber(v[4])
local now = tonumber(ARGV[1])
-- Trần tuyệt đối được cưỡng chế ngay tại đây chứ không chỉ ở phía Go: xoá luôn
-- key để bộ nhớ tự lành, thay vì để một bản ghi chết nằm lại đến hết TTL trượt.
if absExp <= now then
  redis.call('DEL', KEYS[1])
  return nil
end
local ttl = tonumber(ARGV[2])
-- Kẹp TTL trượt vào phần thời gian tuyệt đối còn lại. Không kẹp thì mỗi request
-- lại đẩy hạn ra 7 ngày và key sống lâu hơn cả mốc absolute_exp của chính nó.
local remaining = absExp - now
if remaining < ttl then ttl = remaining end
redis.call('EXPIRE', KEYS[1], ttl)
return v
`)

// peekAndDeleteScript đọc rồi xoá phiên — dùng cho đăng xuất. Bên gọi cần SID và
// user_id để ghi bản ghi audit bên Postgres, mà sau khi DEL thì không lấy được nữa.
//
// KEYS[1] = session:<hash>
// ARGV[1] = hash
var peekAndDeleteScript = goredis.NewScript(`
local v = redis.call('HMGET', KEYS[1], 'sid', 'user_id', 'role', 'absolute_exp')
if not v[1] then return nil end
redis.call('DEL', KEYS[1])
local ptr = 'user_session:' .. v[2]
-- Chỉ xoá con trỏ khi nó còn trỏ đúng vào phiên này. Nếu user vừa đăng nhập lại
-- ở nơi khác, con trỏ đã thuộc về phiên mới và xoá đi sẽ làm mất khả năng thu hồi
-- theo user của phiên đó.
if redis.call('GET', ptr) == ARGV[1] then
  redis.call('DEL', ptr)
end
return v
`)

// revokeUserSIDsScript thu hồi phiên của một user, nhưng chỉ khi phiên đang sống
// nằm trong danh sách SID được yêu cầu.
//
// Việc khớp SID là thứ khiến script này an toàn khi chạy trễ (River purge job có
// thể retry nhiều phút sau): nếu trong lúc đó user đã đăng nhập lại, phiên mới
// mang SID khác và sẽ không bị giết oan.
//
// KEYS[1] = user_session:<user_id>
// ARGV    = danh sách SID cần thu hồi
var revokeUserSIDsScript = goredis.NewScript(`
local h = redis.call('GET', KEYS[1])
if not h then return 0 end
local sid = redis.call('HGET', 'session:' .. h, 'sid')
if not sid then
  -- Con trỏ mồ côi: bản ghi phiên đã hết hạn nhưng con trỏ còn. Dọn luôn.
  redis.call('DEL', KEYS[1])
  return 0
end
for i = 1, #ARGV do
  if ARGV[i] == sid then
    redis.call('DEL', 'session:' .. h)
    redis.call('DEL', KEYS[1])
    return 1
  end
end
return 0
`)

// revokeUserScript thu hồi phiên đang sống của một user VÔ ĐIỀU KIỆN, không khớp
// SID.
//
// Vì sao cần một script riêng thay vì dùng revokeUserSIDsScript: đường khớp SID
// chỉ giết được những phiên mà Postgres còn tin là đang sống, vì danh sách SID
// đến từ `UPDATE sessions ... WHERE revoked_at IS NULL RETURNING id`. Một khi
// Redis và Postgres lệch nhau — hàng audit đã revoked nhưng key Redis còn sống,
// đúng trạng thái mà backstop sinh ra để chữa — lệnh khóa lại khớp không hàng
// nào, trả danh sách rỗng, và không còn đường nào chạm tới key đó nữa. Tài khoản
// bị khóa vẫn dùng được tới hết TTL.
//
// Redis là nguồn phán quyết, nên đường khóa tài khoản phải hỏi Redis "user này
// có phiên nào không" chứ không hỏi Postgres. Đường job retry vẫn phải khớp SID:
// nó chạy trễ và không được giết phiên mà user vừa tạo lại.
//
// KEYS[1] = user_session:<user_id>
var revokeUserScript = goredis.NewScript(`
local h = redis.call('GET', KEYS[1])
if not h then return 0 end
-- Con trỏ mồ côi: bản ghi phiên đã hết hạn nhưng con trỏ còn. Dọn con trỏ và
-- báo 0, vì không có phiên sống nào bị giết ở đây. Trả 1 trong trường hợp này
-- sẽ làm log "đã thu hồi" nói dối về một phiên vốn đã tự chết.
if redis.call('EXISTS', 'session:' .. h) == 0 then
  redis.call('DEL', KEYS[1])
  return 0
end
redis.call('DEL', 'session:' .. h)
redis.call('DEL', KEYS[1])
return 1
`)
