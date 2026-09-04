# Hướng dẫn deploy PaySplit Backend lên VM (Oracle Cloud / VPS)

Runbook đầy đủ từ máy trắng đến API chạy HTTPS. Mỗi bước có lệnh copy-paste được và
giải thích *tại sao* cần bước đó.

**Môi trường tham chiếu**: Oracle Cloud Ampere A1 (ARM64), Ubuntu 26.04 LTS, user `ubuntu`.
Thay `161.118.233.21` bằng IP thật của bạn ở mọi chỗ.

---

## 0. Bối cảnh: vì sao chuyển sang VM

Kiến trúc hiện tại của PaySplit **bắt buộc phải có tiến trình sống lâu**:

| Thành phần | Yêu cầu |
|---|---|
| River Queue | worker engine chạy liên tục |
| PostgreSQL `LISTEN/NOTIFY` | một session giữ mở vĩnh viễn |
| SSE (`/bills/{id}/events`, `/groups/{id}/events`) | kết nối HTTP mở tới 15 phút |
| Background workers (cleanup, retention, metrics) | ticker định kỳ |

Chạy trên serverless (Vercel) thì:

1. **Connection bị nhân theo số instance**, không có trần. Mỗi instance tạo một pool riêng
   → `DB_MAX_CONNS=10` × 10 instance = 100 connection. Đây là nguyên nhân lỗi
   `FATAL: remaining connection slots are reserved` (SQLSTATE 53300).
2. **Instance bị *freeze*, không *shutdown*.** `App.Shutdown()` không bao giờ chạy →
   socket treo lại → backend Postgres nằm `idle` cho tới khi TCP keepalive dọn.
3. Đổi nhà cung cấp DB (Supabase → Aiven) **không bao giờ thắng được** — serverless
   luôn có thể scale vượt qua số slot của bất kỳ plan nào.

Đặt BE và PostgreSQL **trên cùng một VM** giải quyết 5 vấn đề cùng lúc:

| Vấn đề | Sau khi chuyển |
|---|---|
| Cạn connection | 1 tiến trình, pool cố định. `max_connections` do bạn quyết định |
| RTT ~100ms tới DB cloud | Cùng máy → ~0.1ms |
| River / LISTEN / SSE / workers | Chạy đúng như thiết kế |
| Connection ma do không graceful shutdown | SIGTERM thật, `stop_grace_period: 30s` |
| Cold start | Không còn |

---

## 1. Chuẩn bị trước khi bắt đầu

Cần có sẵn trong tay:

- [ ] VM đã tạo, SSH vào được: `ssh -i ssh-key.key ubuntu@161.118.233.21`
- [ ] Quyền truy cập **OCI Console** (để mở firewall lớp ngoài)
- [ ] File `.env.production` trên máy local (file này bị `.gitignore`, không có trong repo)
- [ ] `DATABASE_URL` đầy đủ của Aiven — chỉ cần nếu bạn muốn mang dữ liệu cũ sang
- [ ] Thư mục `deploy/` đã được commit và push lên repo

```bash
# Trên máy LOCAL — commit bộ file deploy trước
cd ~/Documents/namplh/code/PaySplit-BE
git add deploy/
git commit -m "chore: add production deployment stack for single-VM hosting"
git push
```

> Nếu chưa muốn push, có thể `scp -r deploy/ ubuntu@161.118.233.21:~/PaySplit-BE/` sau khi clone.

---

## 2. Mở firewall — **hai lớp độc lập**

Đây là chỗ hầu hết mọi người mắc kẹt với Oracle Cloud. Thiếu lớp nào cũng dẫn tới
`curl: connection timed out` mà **không có log gì cả** ở cả hai phía.

### Lớp 1 — OCI Console (bắt buộc)

`Networking` → `Virtual Cloud Networks` → chọn VCN → `Subnets` → chọn subnet →
`Security Lists` → `Add Ingress Rules`:

| Source CIDR | IP Protocol | Destination Port Range |
|---|---|---|
| `0.0.0.0/0` | TCP | `80` |
| `0.0.0.0/0` | TCP | `443` |

### Lớp 2 — iptables bên trong VM

Image Ubuntu của Oracle ship sẵn bộ rule chỉ cho phép SSH.

```bash
# Xem rule hiện tại, tìm dòng REJECT ... icmp-host-prohibited
sudo iptables -L INPUT --line-numbers -n | head -20
```

Giả sử dòng `REJECT` nằm ở số 6 — chèn rule mới **ngay trước** nó:

```bash
sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 80 -j ACCEPT
sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 443 -j ACCEPT
sudo netfilter-persistent save
```

> ### ⚠️ Cảnh báo bảo mật quan trọng
>
> Docker publish port bằng DNAT ở chain `FORWARD`, **đi vòng qua chain `INPUT`**.
> Nghĩa là bất kỳ port nào bạn khai trong `ports:` sẽ **lộ thẳng ra internet, kể cả khi
> iptables INPUT đang chặn nó**.
>
> Đó là lý do trong `docker-compose.prod.yaml` service `postgres` **không** có mục `ports:` —
> nó chỉ tồn tại trong network nội bộ Docker. **Đừng thêm `ports: ["5432:5432"]` vào,
> kể cả để debug tạm.** Muốn truy cập DB từ ngoài thì dùng SSH tunnel (xem mục 11).

---

## 3. Cài Docker

```bash
sudo apt update && sudo apt install -y docker.io docker-compose-v2 git
sudo usermod -aG docker $USER
```

**Thoát SSH và đăng nhập lại** — nhóm `docker` chỉ có hiệu lực ở session mới. Nếu bỏ qua
bước này bạn sẽ gặp `permission denied while trying to connect to the Docker daemon socket`.

```bash
exit
ssh -i ssh-key-2026-09-04.key ubuntu@161.118.233.21
```

Kiểm tra:

```bash
docker compose version
free -h          # xem RAM; cần >= 2GB để build Go, xem mục 11 nếu ít hơn
uname -m         # aarch64 = ARM64, mọi image dùng ở đây đều có bản ARM64
```

> Nếu `docker compose` không tồn tại (repo Ubuntu 26.04 chưa có gói), dùng:
> `curl -fsSL https://get.docker.com | sudo sh`

---

## 4. Lấy source code lên VM

```bash
cd ~
git clone -b main https://github.com/FintechVSF-Tranning/PaySplit-BE.git
cd PaySplit-BE
```

`.env.production` chứa secret nên bị `.gitignore` — phải copy tay. Chạy lệnh này
**từ máy local**, không phải trên VM:

```bash
scp -i ssh-key-2026-09-04.key .env.production \
    ubuntu@161.118.233.21:~/PaySplit-BE/.env.production
```

---

## 5. Cấu hình secret và điều chỉnh config

### 5.1 File `deploy/.env` — biến cho Docker Compose

File này **chỉ** phục vụ Compose interpolation (`${...}`), không phải config của app.
Compose tự đọc `.env` nằm cùng thư mục với compose file.

```bash
cd ~/PaySplit-BE/deploy
cp .env.example .env
openssl rand -base64 32        # copy kết quả
nano .env
```

```ini
POSTGRES_PASSWORD=<chuỗi vừa sinh ra>
SITE_ADDRESS=161.118.233.21.nip.io
```

**Về `SITE_ADDRESS`**: Caddy cần một *hostname* (không phải IP trần) để xin chứng chỉ
Let's Encrypt. `nip.io` là dịch vụ DNS wildcard: `161.118.233.21.nip.io` phân giải về
đúng IP đó, và Let's Encrypt cấp cert bình thường cho nó.

→ **Có HTTPS mà không cần mua domain.** Coi đây là giải pháp tạm; khi có domain thật
thì trỏ A record về IP rồi đổi `SITE_ADDRESS` thành `api.paysplit.xyz`.
Nếu nip.io gặp sự cố, thay bằng `161.118.233.21.sslip.io`.

### 5.2 File `.env.production` — config của app

Môi trường đã thay đổi hoàn toàn: DB giờ nằm cùng máy, latency ~0.1ms thay vì 100ms.
Các giá trị cũ vốn được chỉnh để chống latency cao giờ **phản tác dụng**.

```bash
nano ~/PaySplit-BE/.env.production
```

```ini
DB_MAX_CONNS=15
DB_MIN_CONNS=3
DB_MAX_CONN_IDLE_MINUTES=15      # cũ: 2 — pool co giãn liên tục, churn vô ích
DB_MAX_CONN_LIFETIME_MINUTES=60
RIVER_POLL_ONLY=false            # cũ: true — LISTEN/NOTIFY nay chạy tốt, bỏ polling mỗi 1s
```

**Không cần sửa `DATABASE_URL`** trong file này — `docker-compose.prod.yaml` ghi đè nó
để trỏ vào Postgres nội bộ (Compose ưu tiên `environment:` hơn `env_file:`).

> Ràng buộc cần giữ: `RIVER_WORKER_COUNT` phải **nhỏ hơn** `DB_MAX_CONNS`, nếu không
> app sẽ fail lúc validate config (`internal/config/config.go`).

---

## 6. Khởi động PostgreSQL và đưa dữ liệu vào

```bash
cd ~/PaySplit-BE
docker compose -f deploy/docker-compose.prod.yaml up -d postgres
docker compose -f deploy/docker-compose.prod.yaml ps    # chờ tới khi healthy
```

Chọn **một** trong hai đường:

### Đường A — Mang dữ liệu cũ từ Aiven sang

Dump mang theo **cả schema, cả dữ liệu, cả bảng `goose_db_version`** — nên sau bước này
**không cần** chạy migration nữa.

```bash
# Dump từ Aiven
docker run --rm -v "$PWD:/out" postgres:18-alpine \
  pg_dump "<DATABASE_URL_AIVEN_ĐẦY_ĐỦ>" --no-owner --no-acl -Fc -f /out/aiven.dump

# Restore vào Postgres nội bộ
docker run --rm -v "$PWD:/in" --network paysplit_default postgres:18-alpine \
  pg_restore --no-owner --no-acl \
  -d "postgres://paysplit:<POSTGRES_PASSWORD>@postgres:5432/paysplit" /in/aiven.dump

rm aiven.dump      # xoá ngay, file này chứa toàn bộ dữ liệu người dùng
```

`--no-owner --no-acl` bỏ qua thông tin owner/permission của role `avnadmin` bên Aiven —
role đó không tồn tại ở DB mới.

### Đường B — Bắt đầu từ database trống

```bash
docker compose -f deploy/docker-compose.prod.yaml --profile tools run --rm migrate up
docker compose -f deploy/docker-compose.prod.yaml --profile tools run --rm migrate status
```

`status` phải liệt kê đủ **16 migration** ở trạng thái applied.

> Service `migrate` nằm trong `profiles: ["tools"]` nên nó **không** khởi động cùng stack —
> chỉ chạy khi bạn gọi thẳng bằng `--profile tools run --rm`.

---

## 7. Bật toàn bộ stack

```bash
cd ~/PaySplit-BE
docker compose -f deploy/docker-compose.prod.yaml up -d --build
docker compose -f deploy/docker-compose.prod.yaml logs -f api
```

Lần build đầu mất khoảng **3–5 phút** trên ARM (biên dịch Go từ đầu). Các lần sau nhanh
hơn nhiều nhờ Docker layer cache.

Chờ đến khi thấy dòng:

```
[Server] PaySplit Backend is ready and listening on 0.0.0.0:8080
```

Caddy sẽ tự xin chứng chỉ Let's Encrypt trong vòng vài giây. Xem tiến trình:

```bash
docker compose -f deploy/docker-compose.prod.yaml logs caddy | grep -i certificate
```

---

## 8. Kiểm chứng

```bash
# Health probe — phải trả 200
curl -si https://161.118.233.21.nip.io/health/ready | head -1

# Endpoint công khai, không cần auth
curl -s https://161.118.233.21.nip.io/api/v1/banks | head -c 200
```

Và kiểm tra **đúng cái đã gây ra toàn bộ vấn đề**:

```bash
docker compose -f deploy/docker-compose.prod.yaml exec postgres \
  psql -U paysplit -d paysplit -c \
  "SELECT count(*) AS dang_dung,
          (SELECT setting FROM pg_settings WHERE name='max_connections') AS toi_da
   FROM pg_stat_activity;"
```

Kết quả mong đợi: khoảng **18–20 / 100**, và **đứng yên** theo thời gian.
Không còn `SQLSTATE 53300`.

Kiểm tra SSE (quan trọng — đây là thứ dễ hỏng nhất qua reverse proxy):

```bash
# Phải thấy event "heartbeat" xuất hiện mỗi 15 giây, KHÔNG bị treo im lặng
curl -N -H "Authorization: Bearer <ACCESS_TOKEN>" \
  https://161.118.233.21.nip.io/api/v1/groups/<GROUP_ID>/events
```

---

## 9. Trỏ Flutter app sang backend mới

```bash
flutter run -t lib/main_staging.dart \
  --dart-define=API_BASE_URL=https://161.118.233.21.nip.io
```

Vì đã có HTTPS thật nên Android không chặn cleartext traffic — không cần
`android:usesCleartextTraffic`.

---

## 10. Dọn dẹp — **đừng bỏ qua bước này**

Đây mới là bước dập tắt hẳn lỗi cũ. Chừng nào các deployment cũ còn sống, chúng còn
tiếp tục ăn connection của Aiven.

1. **Vercel** → project → `Settings` → `Git` → `Disconnect`.
   Trước khi disconnect, ghi lại **Production Branch** và **Source commit** của deployment
   mới nhất — repo không có `vercel.json` hay `api/index.go` trên `main`, nên cần biết
   Vercel thực sự đang chạy code gì.

2. **Truy lùng deployment mồ côi.** Kiểm tra tài khoản Render / Railway của cả nhóm.
   Branch `lampt/testRender` có 2 commit đặc trưng của Render
   (*"default HTTP_HOST to 0.0.0.0"*, *"prioritize PORT environment variable"*) — nếu còn
   service nào sống từ branch đó, nó đang giữ 10 connection Aiven vĩnh viễn.

3. Xác nhận không còn ai cắm vào Aiven, rồi mới xoá instance Aiven.

---

## 11. Vận hành hằng ngày

### Backup — thiết lập ngay, đừng để sau

Tự host PostgreSQL nghĩa là **bạn tự chịu trách nhiệm backup**.

```bash
./deploy/backup.sh                    # chạy thử một lần để chắc chắn hoạt động
crontab -e
```

```cron
0 3 * * * /home/ubuntu/PaySplit-BE/deploy/backup.sh
```

Script giữ lại 14 ngày gần nhất trong `~/paysplit-backups/`. Định kỳ nên copy một bản
ra ngoài VM (Google Drive, S3) — backup nằm cùng máy với dữ liệu gốc thì không cứu được
trường hợp mất cả VM.

### Cập nhật code

```bash
cd ~/PaySplit-BE
git pull
docker compose -f deploy/docker-compose.prod.yaml up -d --build api
```

Nếu lần pull đó có thêm migration mới:

```bash
docker compose -f deploy/docker-compose.prod.yaml --profile tools run --rm migrate up
```

### Lệnh hay dùng

```bash
cd ~/PaySplit-BE
C="docker compose -f deploy/docker-compose.prod.yaml"

$C ps                        # trạng thái các service
$C logs -f api               # theo dõi log app
$C logs --tail=100 caddy     # log reverse proxy / TLS
$C restart api               # restart app (graceful, 30s)
$C down                      # dừng toàn bộ (KHÔNG mất dữ liệu, volume vẫn còn)
$C exec postgres psql -U paysplit -d paysplit   # vào psql
```

### Truy cập DB từ máy local (an toàn, không mở port)

```bash
# Trên máy LOCAL
ssh -i ssh-key-2026-09-04.key -L 5433:localhost:5432 ubuntu@161.118.233.21
```

Sau đó cần Postgres lắng nghe trên host — mặc định compose **không** publish port.
Cách đúng là tunnel qua chính container:

```bash
ssh -i ssh-key-2026-09-04.key ubuntu@161.118.233.21 \
  'docker compose -f ~/PaySplit-BE/deploy/docker-compose.prod.yaml exec -T postgres \
   pg_dump -U paysplit -d paysplit --no-owner -Fc' > local-copy.dump
```

### Thêm swap nếu VM ít RAM

Build Go cần khá nhiều RAM. Nếu `free -h` cho thấy dưới 2GB:

```bash
sudo fallocate -l 4G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

---

## 12. Xử lý sự cố

| Triệu chứng | Nguyên nhân thường gặp | Cách xử lý |
|---|---|---|
| `curl: connection timed out` | Firewall lớp 1 hoặc lớp 2 chưa mở | Làm lại **cả hai** phần ở mục 2 |
| `permission denied ... docker.sock` | Chưa đăng nhập lại sau `usermod` | `exit` rồi SSH vào lại |
| Caddy không xin được cert | Port 80 chưa mở, hoặc `SITE_ADDRESS` không phân giải về IP VM | `dig +short 161.118.233.21.nip.io` phải ra đúng IP |
| `api` restart liên tục | Config validate fail (thiếu biến bắt buộc) | `$C logs api` — thông báo lỗi nói rõ biến nào |
| SSE kết nối được nhưng không có event | Thiếu `flush_interval -1` trong Caddyfile | Kiểm tra `deploy/Caddyfile`, `$C restart caddy` |
| Build bị kill giữa chừng | Hết RAM | Thêm swap (mục 11) |
| `FATAL: role "avnadmin" does not exist` khi restore | Quên `--no-owner --no-acl` | Chạy lại `pg_restore` với đủ hai cờ |
| App chạy nhưng DB trống | Chưa chạy migration | `--profile tools run --rm migrate up` |

---

## 13. Ghi chú quan trọng

- **Oracle Always Free thu hồi instance nhàn rỗi.** Oracle có chính sách reclaim VM
  Always Free để idle kéo dài. Nâng tài khoản lên **Pay As You Go** (vẫn miễn phí với
  tài nguyên trong hạn mức Always Free) là cách chắc chắn nhất để không mất VM giữa kỳ.

- **`flush_interval -1` trong Caddyfile là bắt buộc, không phải tuỳ chọn.** Thiếu nó thì
  reverse proxy sẽ buffer response và SSE treo im lặng không báo lỗi — cực kỳ khó debug.

- **`encode` trong Caddyfile cố tình loại trừ `text/event-stream`.** Nén gzip sẽ buffer
  và làm chết SSE. Đừng đơn giản hoá thành `encode gzip`.

- **`stop_grace_period: 30s`** cho service `api` là để `App.Shutdown()` (ctx 10s) chạy
  xong trước khi container bị SIGKILL. Đây chính là thứ Vercel không bao giờ chạy và gây
  rò rỉ connection.

- **Spec 0010 (`namplh/fix-deploy`) giờ không còn cần thiết cho việc deploy.** Toàn bộ
  chi phí viết lại `app_jobs` + dispatcher + Supabase Realtime tồn tại chỉ vì ràng buộc
  "phải chạy trên Vercel". Ràng buộc đó đã biến mất. Ý tưởng durable job queue và
  version-based sync vẫn giá trị về sau, nhưng không nên để nó chặn việc deploy được ngay.

---

## Checklist

- [ ] Commit + push thư mục `deploy/`
- [ ] Mở ingress 80/443 trên OCI Console
- [ ] Mở iptables 80/443 trong VM + `netfilter-persistent save`
- [ ] Cài Docker, đăng nhập lại SSH
- [ ] Clone repo, `scp` file `.env.production`
- [ ] Tạo `deploy/.env` (`POSTGRES_PASSWORD`, `SITE_ADDRESS`)
- [ ] Chỉnh phần DB trong `.env.production`
- [ ] Khởi động Postgres, restore dump **hoặc** chạy migration
- [ ] `up -d --build`, xác nhận log "ready"
- [ ] `curl /health/ready` trả 200
- [ ] Kiểm tra số connection ổn định (~20/100)
- [ ] Kiểm tra SSE nhận được heartbeat
- [ ] Trỏ Flutter sang URL mới
- [ ] **Disconnect Vercel**
- [ ] **Tắt deployment mồ côi trên Render/Railway**
- [ ] Cài cron backup + chạy thử một lần
