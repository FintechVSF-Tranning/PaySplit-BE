# Rationale: 0009 Group realtime sync v1

> **Ngày**: 2026-08-26 · **Cập nhật**: 2026-09-03 · **Trạng thái**: Proposed
> **Phạm vi**: `internal/modules/group`, `internal/modules/bill`, `internal/modules/settlement` (BE) + `lib/features/groups`, `lib/features/bills`, `lib/features/home` (FE)

## Context

> ⚠️ Premise note: The pain is not “too many Postgres LISTEN connections from each screen.” Screens add HTTP SSE connections, while API listeners consume PostgreSQL connections. Spec 0010 now provides one shared listener connection per API process for `group_events` and `bill_events`. This amendment keeps that one connection, adds `user_events` as a third logical channel, and avoids a `user_events` table that would copy `group_events`.

Roster sync shipped. Home still loads `GET /groups` once. Bill detail after OCR is REST only. `bill.updated` was specified in 0003 and never emitted. Bill submission lock never writes `group_events`. Flutter opens one SSE per open group plus a short OCR SSE, then disposes them. Operators worry that “full realtime” will multiply database connections. Without a decision, the next feature will add a third LISTEN or a per bill socket.

The original roster forces still hold: one way server to phone, a few events per minute, no presence, no chat. Auth still allows one live session per user. Timeout middleware already skips paths ending in `/events`.

---

## 1. Vấn đề cần giải

Trưởng nhóm tạo nhóm 10 người. Mỗi thiết bị chỉ biết danh sách thành viên tại
đúng thời điểm nó gọi `GET /groups/{id}`. Không có kênh nào báo cho các thiết bị
khác biết vừa có người vào, nên người vào trước không thấy người vào sau cho tới
khi kéo refresh.

Yêu cầu: **mọi thiết bị thấy thay đổi với độ trễ thấp nhất có thể**, đúng cả khi
mạng chập chờn, app bị đóng, hay server vừa deploy lại.

Đây **không phải** bài toán chat: luồng dữ liệu một chiều server → client, tần
suất vài sự kiện mỗi phút, không có typing indicator, không có presence.

---

## 2. Chứng cứ từ hệ thống hiện có

Bốn thứ đã có sẵn quyết định phần lớn lựa chọn — chúng khiến phương án được chọn
rẻ hơn nhiều so với việc dựng mới:

1. **`database.LockActiveGroup`** đã là ranh giới điều phối chung của mọi mutation
   phạm vi nhóm (spec 0002 bất biến 1). Khóa dòng `groups` **đã được giữ** trước
   khi bất kỳ thay đổi nào được ghi.
2. **Cả 6 mutation của nhóm đều đi qua đúng một hàm `insertActivity`** ngay trước
   `tx.Commit`. Một điểm móc duy nhất phủ hết vào nhóm, rời nhóm, bị xóa, chuyển
   quyền, đổi tên, giải tán.
3. **Module `bill` đã có SSE Hub + PostgreSQL `LISTEN/NOTIFY` chạy đa instance**
   (`internal/modules/bill/delivery/http/sse_hub.go`), đã được kiểm chứng trên
   production cho luồng OCR.
4. **Timeout middleware đã miễn trừ mọi path kết thúc bằng `/events`**
   (`internal/transport/http/middleware/timeout.go:40`), nên endpoint stream mới
   không cần đụng vào hạ tầng chung.

Ở phía FE: `AuthInterceptor` đã gắn Bearer token và làm mới token khi 401 cho mọi
request đi qua `Dio`.

---

## 3. Các phương án đã cân nhắc

### 3.1 Phương án đơn giản hơn

#### Option A — Chỉ `invalidate` sau mutation + pull-to-refresh

Không đụng backend. Sau mỗi thao tác, thiết bị vừa thao tác tải lại chi tiết nhóm.

- **Ưu**: chi phí gần bằng 0, làm trong một buổi.
- **Nhược**: **không giải quyết đúng bài toán được hỏi.** Nó chỉ sửa cho thiết bị
  của *người thao tác*. Thiết bị của 9 người còn lại vẫn phải refresh thủ công.
- **Kết luận**: cần thiết nhưng không đủ. Đã làm, như lớp thấp nhất của giải pháp.

#### Option B — Polling `GET /groups/{id}` mỗi N giây

- **Ưu**: không có khái niệm mới, không có kết nối sống lâu, không có trạng thái
  ở server.
- **Nhược**, với một nhóm 10 người mở app cùng lúc và N = 5:

  | Chỉ số | Polling (N=5s) | Phương án đã chọn |
  |---|---|---|
  | Request/giây/nhóm | 2 | ~0 |
  | Request/ngày/nhóm (8h dùng) | ~57.600 | ~40 (mỗi kết nối 15 phút + catch-up) |
  | Truy vấn DB mỗi lần | 5 (group, membership, members, balances, batch) | 0 khi không có gì đổi |
  | Băng thông mỗi lần | 2–5 KB (toàn bộ chi tiết nhóm) | ~250 B (một delta) |
  | Độ trễ trung bình | 2,5 s | 50–200 ms |
  | Độ trễ tệ nhất | 5 s | 50–200 ms |

  Nghịch lý của polling: muốn giảm độ trễ phải giảm N, mà giảm N thì chi phí tăng
  tuyến tính — trong khi 99% lần poll trả về **đúng dữ liệu cũ**.
- **Kết luận**: bị loại làm kênh chính. **Vẫn giữ làm lớp dự phòng** qua
  `GET /sync?since=` — nhưng ở dạng rẻ hơn nhiều (xem 3.3).

#### Option C — Polling có điều kiện (ETag / `If-Modified-Since` / trả `304`)

Rẻ hơn B đáng kể: không đổi thì body rỗng.

- **Ưu**: giữ nguyên mô hình request/response, tiết kiệm băng thông.
- **Nhược**: vẫn không giảm được **độ trễ** (vẫn bị chặn dưới bởi N), vẫn tốn một
  vòng TCP + auth + handler mỗi chu kỳ, và quan trọng nhất: **`304` chỉ nói "có
  đổi", không nói "đổi cái gì"** — client buộc phải tải lại và dựng lại toàn bộ
  danh sách, mất luôn khả năng animate riêng dòng vừa thêm.
- **Kết luận**: bị loại. Nhưng ý tưởng "trả về rất ít khi không có gì mới" được
  giữ lại: `/sync?since=N` khi không có gì mới trả đúng
  `{"version":N,"mode":"delta","events":[]}`.

#### Option D — Long-polling

Client giữ một request mở cho tới khi có sự kiện hoặc hết thời gian chờ.

- **Ưu**: độ trễ tốt gần bằng SSE, không cần giao thức mới.
- **Nhược**: mỗi chu kỳ tốn một request đầy đủ (TCP handshake nếu không keep-alive,
  auth middleware, rate-limit, log); mỗi lần có sự kiện là một lần đóng/mở kết nối.
  SSE làm được đúng việc đó trên **một** kết nối, với cùng lượng mã.
- **Kết luận**: bị loại. SSE tốt hơn nghiêm ngặt cho cùng hình dạng bài toán.

#### Option E — Chỉ dùng FCM push làm kênh chính

- **Ưu**: đã có `sessions.fcm_token`, đã có module `notification` và River.
- **Nhược**: FCM **không đảm bảo giao**, **không đảm bảo thứ tự**, có thể bị gộp
  hoặc bị hoãn hàng phút khi máy ở chế độ tiết kiệm pin, và Android/iOS đều giới
  hạn tần suất data-only message. Dùng nó làm kênh chính nghĩa là chấp nhận một
  giao thức đồng bộ không có bảo đảm nào.
- **Kết luận**: bị loại làm kênh chính. Đúng vai trò của nó là **lớp đánh thức khi
  app ở nền** — đã ghi vào lộ trình, chưa làm ở v1.

### 3.2 Phương án khó hơn

#### Option F — WebSocket

- **Ưu**: hai chiều, dùng lại được cho typing/presence sau này.
- **Nhược**: phải tự làm **toàn bộ** những thứ HTTP đang cho không: handshake xác
  thực (token không đi trong header của WS trong trình duyệt), ping/pong giữ kết
  nối, sticky session ở load balancer, backpressure, và một tầng framing riêng
  không đi qua CORS/rate-limit/log/timeout middleware sẵn có. Đổi lại **không có
  lợi ích nào** cho một luồng một chiều.
- **Kết luận**: bị loại. Đây là chi phí trả trước cho một tính năng chưa tồn tại.

#### Option G — MQTT broker (mô hình Zalo/Messenger)

- **Ưu**: mô hình đã được chứng minh ở quy mô hàng trăm triệu người dùng; tiết kiệm
  pin nhờ một kết nối duy nhất cho mọi loại tin.
- **Nhược**: thêm một thành phần hạ tầng phải vận hành, giám sát, backup và bảo
  mật. Lợi ích thật của MQTT chỉ xuất hiện ở mức 10⁶ kết nối đồng thời và khi có
  chat thật sự. PaySplit chưa gần ngưỡng đó.
- **Kết luận**: bị loại vì **không tương xứng quy mô**. Ghi nhận là đích đến nếu
  sản phẩm sau này có nhắn tin trong nhóm.

#### Option H — Firestore / Firebase Realtime Database

Mirror dữ liệu nhóm sang Firestore, để client lắng nghe snapshot listener.

- **Ưu**: gần như không phải viết mã server cho phần realtime.
- **Nhược**: **tách nguồn sự thật khỏi PostgreSQL.** Phải viết lại toàn bộ phân
  quyền nhóm dưới dạng security rules và giữ nó đồng bộ với logic Go — một lớp
  logic trùng lặp, ở nơi khác, bằng ngôn ngữ khác. Thêm chi phí theo lượt đọc, và
  một điểm lệch dữ liệu mới giữa hai hệ.
- **Kết luận**: bị loại. Chi phí đúng đắn dài hạn lớn hơn chi phí viết SSE nhiều lần.

#### Option I — CDC (Debezium + Kafka) đọc WAL của PostgreSQL

- **Ưu**: đây là phiên bản "công nghiệp" của chính kiến trúc đã chọn; không cần
  đụng vào mã mutation.
- **Nhược**: thêm Kafka + connector + schema registry để phục vụ một tính năng có
  vài sự kiện mỗi phút. Và CDC cho ra **thay đổi ở mức dòng**, không phải sự kiện
  nghiệp vụ — vẫn phải viết một tầng dịch từ `UPDATE group_members SET status` sang
  `member_left`.
- **Kết luận**: bị loại. Durable group event log kết hợp transactional `pg_notify` giữ phần cần thiết của ý tưởng đó ở quy mô phù hợp.

#### Option J — Redis Pub/Sub thay cho `pg_notify`

- **Ưu**: quen thuộc, thông lượng cao hơn, không chiếm connection PostgreSQL.
- **Nhược quyết định**: **Redis nằm ngoài transaction.** Publish trước commit thì
  client có thể nhận sự kiện của một transaction sẽ rollback; publish sau commit
  thì tiến trình chết giữa hai bước sẽ làm mất sự kiện. `pg_notify` gọi *bên trong*
  transaction được PostgreSQL đảm bảo **chỉ phát khi COMMIT thành công** — đúng
  thứ ta cần, miễn phí.
- **Kết luận**: bị loại. Đây là điểm mà lựa chọn "kém hiện đại hơn" lại đúng hơn.

#### Option K — Event sourcing đầy đủ (`group_events` là nguồn sự thật)

- **Ưu**: nhật ký trở thành sự thật duy nhất, snapshot chỉ là bản dựng lại.
- **Nhược**: viết lại toàn bộ module `group` đang chạy đúng, mất khả năng truy vấn
  quan hệ trực tiếp (`v_member_balances`, các JOIN của settlement), và không giải
  quyết thêm bất cứ điều gì cho bài toán đang hỏi.
- **Kết luận**: bị loại. Nhật ký ở đây cố ý là **dẫn xuất**, không phải nguồn sự
  thật — mất sạch `group_events` thì hệ thống vẫn đúng, chỉ là mọi client nhận
  snapshot thay vì delta.

### 3.3 Phương án đã chọn — SSE + durable event log + transactional notify + version fencing

Ba lớp xếp chồng, tin cậy giảm dần:

| Lớp | Cơ chế | Vai trò |
|---|---|---|
| 1 | `GET /groups/{id}` (REPEATABLE READ) | Trạng thái xuất phát + version |
| 2 | `GET /groups/{id}/events` (SSE) | Kênh nóng, 50–200 ms, **được phép mất gói** |
| 3 | `GET /groups/{id}/sync?since=` | Hàn mọi lỗ hổng, và là đường duy nhất được tin khi version không liền mạch |

Vì lớp 3 luôn có mặt, lớp 2 đứt bất cứ lúc nào cũng **không làm sai dữ liệu** — nó
chỉ làm tăng độ trễ. Đó là tính chất khiến phương án này an toàn hơn hẳn mọi
phương án "kênh đẩy là nguồn sự thật".

---

## 4. Bốn quyết định bên trong, và vì sao không đơn giản hơn

### 4.1 Version bump bằng `UPDATE ... RETURNING`, không phải `BIGSERIAL`

Đây là quyết định quan trọng nhất của cả thiết kế.

Với một sequence toàn cục, hai transaction có thể lấy số 5 và 6 nhưng **commit
ngược thứ tự**. Client đọc `WHERE version > 4` đúng khoảnh khắc đó sẽ thấy 6, ghi
nhận mình đang ở version 6, và **vĩnh viễn không bao giờ thấy 5**. Lỗi này không
tái hiện được trong test tuần tự và chỉ xuất hiện dưới tải — đúng loại lỗi đắt nhất.

`UPDATE groups SET roster_version = roster_version + 1 WHERE id = $1 RETURNING`
khóa dòng `groups`: transaction sau phải chờ transaction trước commit xong. Và vì
`LockActiveGroup` **đã** giữ khóa đó từ đầu mỗi mutation, chi phí serialize là
**bằng không** — ta chỉ đang tận dụng một bất biến đã tồn tại.

Test `TestEmitGroupEvent_ConcurrentJoinsProduceAGaplessSequence` cho 10 người vào
đồng thời và khẳng định dãy version là 1..10, không hở, không trùng.

**Vì sao không dùng `updated_at`?** Timestamp có xung đột trong cùng mili giây,
phụ thuộc đồng hồ, và không cho phép phát hiện "tôi thiếu đúng một sự kiện".

### 4.2 Có nhật ký sự kiện, không chỉ đẩy "đã có thay đổi"

Phương án đơn giản hơn: chỉ đẩy `{group_id, version}` và để client tự gọi lại
`GET /groups/{id}`. Không cần bảng `group_events`, không cần job dọn.

Đã cân nhắc nghiêm túc. Lý do vẫn chọn nhật ký:

| | Đẩy "gầy" (không nhật ký) | Đẩy delta (có nhật ký) |
|---|---|---|
| 10 người vào nhóm 20 người | 200 request tải lại chi tiết nhóm | 0 request |
| Băng thông | 200 × ~3 KB = 600 KB | 200 × ~250 B = 50 KB |
| Reconnect sau 30 giây mất mạng | Luôn là một snapshot đầy đủ | Vài delta, thường là 0 |
| Client dựng lại UI | Toàn bộ danh sách | Đúng một dòng |

Với nhóm đông người trong một chuyến đi — chính kịch bản của sản phẩm — chênh lệch
là bậc độ lớn. Chi phí đổi lại: một bảng, một job dọn ~10 dòng, và một trường giữ
7 ngày.

Đáng nói: **cả hai đều được cài**. Envelope `pg_notify` vượt 7000 byte sẽ tự rơi
về bản "gầy", và client xử lý nhánh đó bằng đúng cơ chế catch-up vốn đã phải đúng.

### 4.3 Snapshot đọc trong REPEATABLE READ

`GetGroupDetail` đọc version và danh sách thành viên bằng nhiều câu lệnh. Ở mức
READ COMMITTED, **mỗi câu lệnh có ảnh chụp riêng**: một mutation xen vào giữa hai
câu lệnh sẽ tạo ra snapshot mang version N nhưng đã chứa thành viên của version
N+1. Client sau đó nhận event N+1, thấy nó "đã áp rồi"… nhưng thực ra chỉ áp một
phần, và giữ state sai vĩnh viễn.

Đọc ngược thứ tự cũng không cứu được: version cũ hơn danh sách thì client áp lại
một sự kiện (chấp nhận được nếu client idempotent), version mới hơn danh sách thì
client mất sự kiện (không chấp nhận được).

Một transaction `REPEATABLE READ, READ ONLY` khép kín khe hở đó, **không khóa gì**,
và tốn thêm đúng một round-trip `BEGIN`/`COMMIT`.

### 4.4 SSE và `/sync` dùng chung một hình dạng sự kiện

Thân mỗi frame SSE mang đúng shape với phần tử `events` của `/sync`:
`{version, type, data}`. Client vì thế dùng **một** hàm giải mã cho cả hai kênh.

Phương án đơn giản hơn — đọc version từ trường `id:` của SSE — bị loại vì proxy có
quyền bỏ trường đó, và vì nó tạo ra hai đường giải mã phải giữ đồng bộ bằng tay.
Trường `id:` vẫn được ghi, nhưng chỉ dành cho client dùng `EventSource` chuẩn.

---

## 5. Những gì cố ý **chưa** làm

| Hạng mục | Lý do hoãn |
|---|---|
| FCM data-only đánh thức app ở nền | Là lớp bổ sung, không đổi giao thức. Làm sau khi giao thức đã ổn định trên thực địa. |
| Đưa hóa đơn / công nợ lên cùng stream nhóm as full deltas | Invalidation plus REST GET is enough. A bill event log is a later spec if refetch cost is measured. |
| Presence ("ai đang mở nhóm") | Cần kênh hai chiều — đây mới là lúc WebSocket có lý. |
| `user_events` table / per user version | Roster already has a log. Home and bill heal by refetch. A second log would duplicate `group_events`. |

---

## 6. Rủi ro và cách chặn

| Rủi ro | Cách chặn |
|---|---|
| Client chậm làm nghẽn server | Legacy roster streams may rely on version fencing. The user stream closes a slow subscriber instead of silently dropping an invalidation that has no sequence number. Reconnect then refetches mounted surfaces. |
| Khe hở giữa lúc đọc snapshot và lúc đăng ký | Handler `Subscribe` **trước**, đọc trạng thái **sau**; mọi sự kiện có `version <= lastSent` bị bỏ qua. |
| Phiên bị thu hồi vẫn giữ stream | Revocation publishes `session_ended` to every process. `maxConnectionAge` remains the bounded fallback. |
| Thành viên bị xóa vẫn nghe được sự kiện nhóm | The final roster audience includes the removed user. Later transactions exclude that user. The legacy group stream closes for `membership_ended`. |
| Deploy làm hàng loạt client reconnect | Backoff **có jitter** ±30% ở FE; và nhờ `since`, phần lớn lần reconnect trả về 0 sự kiện thay vì snapshot. |
| Nhật ký phình to | Job dọn theo `GROUP_EVENT_RETENTION_DAYS` (mặc định 7). Client tụt xa hơn thế nhận snapshot — vẫn đúng, chỉ đắt hơn. |
| Cạn connection pool | Mỗi instance dùng **một** connection cho `LISTEN` trên cả ba kênh; handler chỉ mượn pool lúc đọc rồi trả ngay. |

---

## 7. Quyết định

Chọn **SSE + durable group event log + transactional `pg_notify` + version fencing**.

Nó không phải phương án đơn giản nhất, cũng không phải phương án mạnh nhất. Nó là
phương án duy nhất trong danh sách thỏa đồng thời bốn điều kiện:

1. Đạt độ trễ **50–200 ms** — điều mọi phương án polling không đạt được.
2. **Không có kịch bản nào làm dữ liệu sai**, kể cả khi kênh đẩy hỏng hoàn toàn —
   điều mọi phương án "push là nguồn sự thật" không đảm bảo.
3. **Không thêm một thành phần hạ tầng nào** — không broker, không Redis, không
   Kafka, không nguồn sự thật thứ hai.
4. Dùng lại **4 thứ đã có sẵn và đã được kiểm chứng** trong chính repo này
   (mục 2), nên không cần thêm broker hoặc nguồn sự thật mới.

## Options considered

The following amendment dated 2026-09-03 extends the shipped roster decision to one user stream.

#### Option 1: Keep per resource SSE

Add the missing bill and lock signals to the existing resource streams, then use polling or another stream for Home.

**Pros**: This is the smallest backend change and leaves roster delivery untouched.

**Cons**: Flutter still opens a stream for each active resource. Home needs a separate mechanism. The HTTP connection count still grows with navigation, even though spec 0010 already fixed the database listener count.

#### Option 2: One user SSE with a mutable group membership cache

Open `GET /api/v1/users/me/events`. At subscribe time, load the caller’s groups into the hub and update that in memory set when membership events arrive.

**Pros**: One phone connection and no recipient list in database notifications.

**Cons**: Correctness depends on event order and on every process updating the cache. A self join can be missed because the new member is not in the old set. A removal can leak later group events if cache removal races dispatch. Reconnect, process restart, and missed notifications all require a cache rebuild protocol.

#### Option 3: One user SSE with transaction sourced audiences and invalidation (chosen)

Keep the durable roster delta and `/sync`. Add a small `user_events` notification channel for Home, bill, lock, settlement, and stream control. Each mutation derives its recipient user IDs while it still owns the database transaction and puts that internal audience into `pg_notify`. The hub indexes subscribers only by `user_id`, `sid`, and `stream_id`; it does not maintain group membership.

Spec 0010’s shared listener subscribes to `group_events`, `bill_events`, and `user_events` on the same dedicated PostgreSQL connection. The new channel does not mean a new connection or a new table.

**Pros**: One SSE per signed in device, one database listener connection per API process, no membership cache race, and no new durable event log. Roster keeps efficient deltas. Other surfaces use their existing REST reads as the source of truth.

**Cons**: Mutations do one bounded recipient query. Invalidation may cause an extra REST read. Non roster events are not replayable, so reconnect must refetch the surfaces that are currently mounted.

#### Option 4: Add a durable per user event log

Insert every group, bill, and settlement change into `user_events (user_id, seq)` and stream that log.

**Pros**: It gives every device a single replay cursor and removes ambiguity after a disconnect.

**Cons**: Every group mutation fans out one stored row per member. Retention, compaction, and backfill become part of the product. It duplicates the existing `group_events` log when refetch already repairs Home and bill state.

#### Option 5: Move fanout to a hosted realtime service

Move subscriptions outside the API and PostgreSQL listener.

**Pros**: API processes no longer own long lived client fanout.

**Cons**: Authorization and lifecycle rules move into another system. The current long lived Go API can solve this with infrastructure it already operates, so a vendor is not justified for this slice.

## Rationale

### Why option 3

The requirement has two different counts. Flutter should not multiply HTTP streams as the user moves between Home, group, and bill. API replicas should not multiply database connections by resource type. A session scoped user SSE solves the first. Extending spec 0010’s fixed channel registry on its existing connection solves the second.

The mutable membership cache was the tempting implementation, but it makes authorization depend on transient process state. Transaction sourced audiences are simpler to prove. The transaction already knows the group, bill, and affected users. `pg_notify` is issued inside that transaction, so PostgreSQL releases the notification only after commit and discards it on rollback. Public SSE frames never contain the audience.

Server side topic subscription was also considered after the user stream was chosen. It does not reduce HTTP connections because every active device still needs its own transport. With SSE it also needs a second REST control surface. Flutter therefore keeps one transport and uses an in memory interest registry for local screen `sub/unsub`. The server continues to authorize and route by transaction sourced audience.

Settlement uses invalidation rather than new deltas because its existing REST reads already combine payable, receivable, payment, debt, and bill state. The exact mutation matrix includes payment creation, proof submission, confirm, reject, reminder, and persisted worker changes. OCR keeps its specific frame because the waiter needs progress, including `queued`, but always confirms current state through the bill read.

### Recovery decisions

The user stream is deliberately an invalidation channel, not a replay log. Therefore recovery is explicit:

1. New subscribe is admitted only while the local listener is healthy. Every successful subscribe or reconnect receives `ready`, and Flutter refetches only registered interests.
2. A gap in roster versions still uses `GET /groups/{id}/sync?since=`.
3. A listener reset from spec 0010 closes local SSE subscribers. A full subscriber buffer also closes that subscriber. Neither condition silently drops an invalidation.
4. OCR current state, including `queued`, is confirmed by `GET /bills/{id}` after reconnect or after the existing timeout.
5. Lock and settlement frames invalidate existing reads. They do not invent a roster version for data that is not roster state.
6. Failed REST refresh remains dirty and retries while last good data stays visible. Bounded client buffers collapse to a mounted refetch or roster `/sync`, so recovery memory cannot grow without limit.

This accepts duplicate notifications and duplicate GETs. It does not accept stale state hidden behind a connection that appears healthy.

### Session and rollout decisions

One live session per user does not by itself close an already authenticated SSE on another API replica. Session replacement and all revocation paths therefore publish a targeted control notification. A new stream for the same `sid` also publishes a replace instruction so only the newest `stream_id` remains active across replicas.

The Flutter provider lives above the authenticated navigation shell and below auth state. Screens subscribe to in memory frames and never own the transport. Background lifecycle closes the foreground stream; resume reconnects and refetches. Legacy resource streams remain behind exclusive feature flags during the strangler period. Only `404` or `501` before the first `ready` can select legacy mode. A temporary listener failure never opens both modes. The API returns `410 Gone` only after the configured minimum app version uses the user stream and telemetry records no legacy route connection in any version bucket for 30 consecutive days.

The listener must use a direct PostgreSQL endpoint or a session mode pooler. Transaction mode pooling is incompatible because `LISTEN` is session state. `DATABASE_LISTENER_URL` therefore defaults to `DATABASE_URL` for simple deployments but can point to a direct or session endpoint while request traffic uses transaction pooling. A notification round trip smoke test proves this path before production traffic is enabled.
