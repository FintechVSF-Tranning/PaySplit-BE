# Rationale: 0009 Group realtime sync v1

> **Ngày**: 2026-08-26 · **Trạng thái**: Implemented
> **Phạm vi**: `internal/modules/group` (BE) + `lib/features/groups` (FE)

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
- **Kết luận**: bị loại. Transactional outbox chính là ý tưởng đó ở quy mô phù hợp.

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

### 3.3 Phương án đã chọn — SSE + transactional outbox + version fencing

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
| `GET /me/events` — một stream duy nhất mỗi thiết bị | Đúng mô hình của Slack/Telegram/Zalo và sẽ giảm số kết nối về 1, nhưng cần theo dõi tập nhóm của user thay đổi theo thời gian thực. Màn danh sách nhóm hiện dùng refetch, đủ dùng. |
| FCM data-only đánh thức app ở nền | Là lớp bổ sung, không đổi giao thức. Làm sau khi giao thức đã ổn định trên thực địa. |
| Đưa hóa đơn / công nợ lên cùng stream nhóm | Cấu trúc đã cho phép (chỉ là thêm `event_type`), nhưng mỗi loại cần định nghĩa payload riêng — làm cùng lúc sẽ khó kiểm chứng. |
| Presence ("ai đang mở nhóm") | Cần kênh hai chiều — đây mới là lúc WebSocket có lý. |

---

## 6. Rủi ro và cách chặn

| Rủi ro | Cách chặn |
|---|---|
| Client chậm làm nghẽn server | Buffer 32 sự kiện mỗi kết nối, đầy thì **bỏ** sự kiện. An toàn vì version fencing khiến client tự phát hiện và gọi `/sync`. |
| Khe hở giữa lúc đọc snapshot và lúc đăng ký | Handler `Subscribe` **trước**, đọc trạng thái **sau**; mọi sự kiện có `version <= lastSent` bị bỏ qua. |
| Phiên bị thu hồi vẫn giữ stream | `maxConnectionAge` 15 phút buộc mở lại, và lần mở lại đi qua middleware auth đầy đủ. |
| Thành viên bị xóa vẫn nghe được sự kiện nhóm | Handler phát `close` kèm `membership_ended` / `group_archived` rồi đóng. |
| Deploy làm hàng loạt client reconnect | Backoff **có jitter** ±30% ở FE; và nhờ `since`, phần lớn lần reconnect trả về 0 sự kiện thay vì snapshot. |
| Nhật ký phình to | Job dọn theo `GROUP_EVENT_RETENTION_DAYS` (mặc định 7). Client tụt xa hơn thế nhận snapshot — vẫn đúng, chỉ đắt hơn. |
| Cạn connection pool | Mỗi instance dùng **một** connection cho `LISTEN`; handler chỉ mượn pool lúc đọc rồi trả ngay. |

---

## 7. Quyết định

Chọn **SSE + transactional outbox + version fencing**.

Nó không phải phương án đơn giản nhất, cũng không phải phương án mạnh nhất. Nó là
phương án duy nhất trong danh sách thỏa đồng thời bốn điều kiện:

1. Đạt độ trễ **50–200 ms** — điều mọi phương án polling không đạt được.
2. **Không có kịch bản nào làm dữ liệu sai**, kể cả khi kênh đẩy hỏng hoàn toàn —
   điều mọi phương án "push là nguồn sự thật" không đảm bảo.
3. **Không thêm một thành phần hạ tầng nào** — không broker, không Redis, không
   Kafka, không nguồn sự thật thứ hai.
4. Dùng lại **4 thứ đã có sẵn và đã được kiểm chứng** trong chính repo này
   (mục 2), nên phần thực sự mới chỉ là ~500 dòng Go và ~400 dòng Dart.
