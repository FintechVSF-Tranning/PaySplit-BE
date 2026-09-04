# Hướng dẫn deploy PaySplit Backend lên VM (Oracle Cloud / VPS)

Runbook đầy đủ từ máy trắng đến API chạy HTTPS. Mỗi bước có lệnh copy-paste được và
giải thích _tại sao_ cần bước đó.

**Môi trường tham chiếu**: Oracle Cloud Ampere A1 (ARM64), Ubuntu 26.04 LTS, user `ubuntu`.
Thay `161.118.233.21` bằng IP thật của bạn ở mọi chỗ.

---

## 0. Bối cảnh: vì sao chuyển sang VM

Kiến trúc hiện tại của PaySplit **bắt buộc phải có tiến trình sống lâu**:

| Thành phần                                        | Yêu cầu                      |
| ------------------------------------------------- | ---------------------------- |
| River Queue                                       | worker engine chạy liên tục  |
| PostgreSQL `LISTEN/NOTIFY`                        | một session giữ mở vĩnh viễn |
| SSE (`/bills/{id}/events`, `/groups/{id}/events`) | kết nối HTTP mở tới 15 phút  |
| Background workers (cleanup, retention, metrics)  | ticker định kỳ               |

Chạy trên serverless (Vercel) thì:

1. **Connection bị nhân theo số instance**, không có trần. Mỗi instance tạo một pool riêng
   → `DB_MAX_CONNS=10` × 10 instance = 100 connection. Đây là nguyên nhân lỗi
   `FATAL: remaining connection slots are reserved` (SQLSTATE 53300).
2. **Instance bị _freeze_, không _shutdown_.** `App.Shutdown()` không bao giờ chạy →
   socket treo lại → backend Postgres nằm `idle` cho tới khi TCP keepalive dọn.
3. Đổi nhà cung cấp DB (Supabase → Aiven) **không bao giờ thắng được** — serverless
   luôn có thể scale vượt qua số slot của bất kỳ plan nào.

Đặt BE và PostgreSQL **trên cùng một VM** giải quyết 5 vấn đề cùng lúc:

| Vấn đề                                   | Sau khi chuyển                                                  |
| ---------------------------------------- | --------------------------------------------------------------- |
| Cạn connection                           | 1 tiến trình, pool cố định. `max_connections` do bạn quyết định |
| RTT ~100ms tới DB cloud                  | Cùng máy → ~0.1ms                                               |
| River / LISTEN / SSE / workers           | Chạy đúng như thiết kế                                          |
| Connection ma do không graceful shutdown | SIGTERM thật, `stop_grace_period: 30s`                          |
| Cold start                               | Không còn                                                       |

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
git push origin dev
```

> Nếu chưa muốn push, có thể `scp -r deploy/ ubuntu@161.118.233.21:~/PaySplit-BE/` sau khi clone.

---

## 2. Mở firewall — **hai lớp độc lập**

Đây là chỗ hầu hết mọi người mắc kẹt với Oracle Cloud. Thiếu lớp nào cũng dẫn tới
`curl: connection timed out` mà **không có log gì cả** ở cả hai phía.

### Lớp 1 — OCI Console (bắt buộc)

**Bước 1.** Menu ☰ → `Networking` → `Virtual cloud networks` → chọn VCN của bạn.

**Bước 2.** Tab `Subnets` → click vào subnet có cột **Subnet Access** ghi **`Public (Regional)`**.

> Phải là **public** subnet, không phải private. VM có IP public thì chắc chắn nằm ở
> public subnet. Nếu click nhầm private subnet thì rule thêm vào sẽ không có tác dụng gì.

**Bước 3.** Trong trang chi tiết subnet, kéo xuống mục **Security** → bảng
**Security Lists** → click vào security list (thường tên là
`Default Security List for <tên-vcn>`).

**Bước 4.** Tab **Security Rules** → nút **Add Ingress Rules**.

> 💡 Bạn sẽ thấy sẵn một rule cho port `22` trong danh sách — đó là dấu hiệu vào đúng chỗ,
> vì chính rule đó đang cho phép bạn SSH vào máy.

**Bước 5.** Điền rule thứ nhất (HTTP):

| Trường                 | Giá trị                             |
| ---------------------- | ----------------------------------- |
| Stateless              | ❌ **không tick** (để stateful)     |
| Source Type            | `CIDR`                              |
| Source CIDR            | `0.0.0.0/0`                         |
| IP Protocol            | `TCP`                               |
| Source Port Range      | **để trống** (nghĩa là All)         |
| Destination Port Range | `80`                                |
| Description            | `HTTP - Caddy + Let's Encrypt ACME` |

**Bước 6.** Bấm **+ Another Ingress Rule**, điền rule thứ hai y hệt nhưng đổi:

| Trường                 | Giá trị                |
| ---------------------- | ---------------------- |
| Destination Port Range | `443`                  |
| Description            | `HTTPS - PaySplit API` |

**Bước 7.** Bấm **Add Ingress Rules** ở cuối form.

> ### Port 80 là bắt buộc, đừng bỏ qua
>
> Nhiều người chỉ mở `443` vì nghĩ "chỉ cần HTTPS". Nhưng Let's Encrypt dùng
> **HTTP-01 challenge qua port 80** để xác minh quyền sở hữu domain — thiếu nó Caddy
> không xin được chứng chỉ và bạn không bao giờ có HTTPS.

**Không cần đụng tới Internet Gateway hay Route Table.** Việc SSH vào được đã chứng minh
IGW và route `0.0.0.0/0` đang hoạt động đúng.

**Kiểm tra xem instance có gắn NSG không.** Nếu có Network Security Group, phải mở port
ở đó nữa: `Compute` → `Instances` → chọn instance → `Attached VNICs` → cột `NSGs`.
Trống thì bỏ qua, Security List là đủ.

#### Kiểm chứng ngay sau bước này

Chạy **từ máy local**:

```bash
nc -zv 161.118.233.21 80
```

| Kết quả                | Ý nghĩa                                                                                |
| ---------------------- | -------------------------------------------------------------------------------------- |
| `Connection refused`   | ✅ **Đúng** — firewall đã thông, chỉ là chưa có gì lắng nghe port 80 (Caddy chưa chạy) |
| `Connection timed out` | ❌ Vẫn bị chặn — xem lại Security List, và cả NSG nếu có                               |

Phân biệt `refused` với `timed out` là mẹo quan trọng nhất khi debug firewall:
`refused` nghĩa là gói tin **đã tới được máy**; `timed out` nghĩa là nó bị nuốt giữa đường.

### Lớp 2 — iptables bên trong VM

Image Ubuntu của Oracle ship sẵn bộ rule chỉ cho phép SSH.

```bash
# Xem rule hiện tại, tìm SỐ DÒNG của rule REJECT ... icmp-host-prohibited
sudo iptables -L INPUT --line-numbers -n | head -20
```

Output điển hình trên image Oracle Ubuntu — chú ý cột `num` của dòng `REJECT`:

```
num  target     prot opt source               destination
1    ACCEPT     all  --  0.0.0.0/0            0.0.0.0/0   state RELATED,ESTABLISHED
2    ACCEPT     icmp --  0.0.0.0/0            0.0.0.0/0
3    ACCEPT     all  --  0.0.0.0/0            0.0.0.0/0
4    ACCEPT     tcp  --  0.0.0.0/0            0.0.0.0/0   state NEW tcp dpt:22
5    REJECT     all  --  0.0.0.0/0            0.0.0.0/0   reject-with icmp-host-prohibited
```

Chèn rule mới **ngay trước** dòng REJECT. Thay `N` bằng số dòng REJECT bạn thấy
(ở ví dụ trên là `5`):

```bash
sudo iptables -I INPUT 5 -m state --state NEW -p tcp --dport 80 -j ACCEPT
sudo iptables -I INPUT 5 -m state --state NEW -p tcp --dport 443 -j ACCEPT
sudo iptables -L INPUT --line-numbers -n | head -20   # xác nhận cả 2 rule đứng TRƯỚC REJECT
```

Lưu lại để rule sống sót qua reboot:

```bash
sudo netfilter-persistent save
```

Nếu báo `command not found` (image minimized thường thiếu gói này):

```bash
sudo apt install -y iptables-persistent    # hỏi lưu rule hiện tại → chọn Yes
# hoặc lưu thủ công:
sudo iptables-save | sudo tee /etc/iptables/rules.v4 > /dev/null
```

> ### ⚠️ Cảnh báo bảo mật quan trọng
>
> Docker publish port bằng DNAT ở chain `FORWARD`, **đi vòng qua chain `INPUT`**.
> Nghĩa là bất kỳ port nào bạn khai trong `ports:` sẽ **lộ thẳng ra internet, kể cả khi
> iptables INPUT đang chặn nó**.
>
> Đó là lý do trong `docker-compose.prod.yaml`, PostgreSQL được bind là
> **`"127.0.0.1:5432:5432"`** chứ không phải `"5432:5432"`. Tiền tố `127.0.0.1` giới hạn
> cổng đó vào loopback của VM — từ internet không với tới, và cách duy nhất để vào là
> SSH tunnel (xem mục 11).
>
> **Đừng bao giờ bỏ tiền tố `127.0.0.1` đi.** `"5432:5432"` sẽ khiến PostgreSQL lộ thẳng
> ra internet dù iptables đang chặn.
>
> Hệ quả ngược lại cũng đúng: vì Docker đi vòng qua `INPUT`, **Caddy trong container vẫn
> nhận được traffic 80/443 kể cả khi bạn chưa thêm rule iptables ở trên**. Lớp quyết định
> thực sự với container là **Security List trên OCI Console**. Vẫn nên thêm rule iptables
> cho nhất quán và để phòng khi sau này bạn chạy service trực tiếp trên host — nhưng khi
> debug "port bị chặn", hãy nghi ngờ Security List trước, không phải iptables.

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
git clone -b dev https://github.com/FintechVSF-Tranning/PaySplit-BE.git
cd PaySplit-BE
git branch --show-current      # xác nhận đang ở dev
```

> **Vì sao `dev`**: đây là nhánh tích hợp, nơi mọi feature branch được merge vào trước.
> `main` chỉ nhận code qua PR từ `dev` nên luôn chậm hơn. Deploy `dev` nghĩa là bạn chạy
> đúng thứ cả nhóm đang làm việc trên đó.
>
> Đổi nhánh sau này:
>
> ```bash
> cd ~/PaySplit-BE && git fetch origin && git checkout main && git pull origin main
> docker compose -f deploy/docker-compose.prod.yaml up -d --build api
> ```

`.env.production` chứa secret nên bị `.gitignore` — phải copy tay. Chạy lệnh này
**từ máy local**, không phải trên VM:

```bash
cd ~/Documents/namplh/code/PaySplit-BE
scp -i ~/Downloads/vps/ssh-key-2026-09-04.key .env.production \
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
openssl rand -hex 32           # copy kết quả
nano .env
```

```ini
POSTGRES_PASSWORD=<chuỗi vừa sinh ra>
SITE_ADDRESS=161.118.233.21.nip.io
```

> ### ⚠️ Phải dùng `-hex`, đừng dùng `-base64`
>
> `openssl rand -base64 32` sinh alphabet Base64 có chứa `/`, `+`, `=`. Dấu `/` **kết thúc
> phần authority của URI**, làm vỡ chuỗi kết nối mà compose dựng ra:
>
> ```yaml
> DATABASE_URL: postgres://paysplit:${POSTGRES_PASSWORD}@postgres:5432/paysplit?sslmode=disable
> ```
>
> Triệu chứng điển hình:
> ```
> pg_restore: error: invalid integer value "Uq6V9ss..." for connection option "port"
> ```
> Parser đọc `paysplit` thành host và phần trước dấu `/` thành port. App cũng chết đúng
> kiểu đó, không riêng gì `pg_restore`.
>
> `openssl rand -hex 32` chỉ cho ra `0-9a-f` — an toàn tuyệt đối trong URL, và 256 bit
> entropy vẫn thừa sức.
>
> **Nếu lỡ tạo mật khẩu có `/` rồi**: sửa file `.env` thôi là *không đủ*, vì
> `POSTGRES_PASSWORD` chỉ có tác dụng lúc initdb lần đầu. Volume đã tồn tại nên phải
> đổi trực tiếp trong database:
>
> ```bash
> docker compose -f deploy/docker-compose.prod.yaml exec postgres \
>   psql -U paysplit -d paysplit -c "ALTER USER paysplit PASSWORD '<mật khẩu mới>';"
> nano deploy/.env      # cập nhật POSTGRES_PASSWORD cho khớp
> docker compose -f deploy/docker-compose.prod.yaml up -d
> ```

**Về `SITE_ADDRESS`**: Caddy cần một _hostname_ (không phải IP trần) để xin chứng chỉ
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
docker run --rm -v "$PWD:/in" --network paysplit_default \
  -e PGPASSWORD='<POSTGRES_PASSWORD>' postgres:18-alpine \
  pg_restore --no-owner --no-acl -h postgres -U paysplit -d paysplit /in/aiven.dump

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
  --dart-define=API_BASE_URL=https://161.118.233.21.nip.io/api/v1
```

> ### Phải có hậu tố `/api/v1`
>
> Flutter khai báo mọi endpoint ở dạng tương đối (`/auth/sign-in`, `/groups/{id}`,
> `/bills/{id}/events` — xem `lib/core/constants/api_endpoints.dart`), và Dio chỉ nối
> `baseUrl + path`. Prefix `/api/v1` nằm ở **base URL**, không nằm trong từng endpoint.
>
> Đối chiếu giá trị mặc định trong `lib/main_staging.dart`:
> `https://paysplitbe.vercel.app/api/v1` — cũng có hậu tố này.
>
> Thiếu `/api/v1` thì app build và chạy bình thường, chỉ là **mọi request đều 404** —
> triệu chứng trông giống lỗi backend nên rất dễ đi lạc hướng khi debug.

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
   (_"default HTTP_HOST to 0.0.0.0"_, _"prioritize PORT environment variable"_) — nếu còn
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
git pull origin dev
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

### Kết nối DataGrip (hoặc DBeaver / pgAdmin) tới DB production

#### Nguyên tắc: PostgreSQL **không** mở ra internet

Trong `docker-compose.prod.yaml`, Postgres được bind như sau:

```yaml
ports:
  - "127.0.0.1:5432:5432"
```

Tiền tố `127.0.0.1` là phần quan trọng nhất của dòng này:

| Cách viết               | Hậu quả                                                                                                                |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `"127.0.0.1:5432:5432"` | ✅ Chỉ bind loopback của VM. Từ internet **không** với tới. Muốn vào phải qua SSH tunnel.                              |
| `"5432:5432"`           | ❌ Bind `0.0.0.0`. Docker DNAT đi vòng qua iptables INPUT → **PostgreSQL lộ thẳng ra internet dù firewall đang chặn.** |

Nói cách khác: cổng 5432 chỉ tồn tại _bên trong_ VM. SSH tunnel là con đường duy nhất
đi vào, và nó dùng lại đúng khoá SSH bạn đã có — không cần mở thêm port nào trên OCI.

#### Lấy mật khẩu DB

```bash
ssh -i ssh-key-2026-09-04.key ubuntu@161.118.233.21 \
  'grep POSTGRES_PASSWORD ~/PaySplit-BE/deploy/.env'
```

Trên máy local, đảm bảo quyền file khoá đủ chặt (nếu không SSH sẽ từ chối dùng):

```bash
chmod 600 ssh-key-2026-09-04.key
```

#### Cách 1 — Dùng SSH tunnel tích hợp của DataGrip (khuyến nghị)

DataGrip tự dựng và tự đóng tunnel theo vòng đời connection, không phải giữ terminal riêng.

`+` → `Data Source` → `PostgreSQL`, rồi điền **hai** tab:

**Tab `General`:**

| Trường         | Giá trị                                |
| -------------- | -------------------------------------- |
| Host           | `localhost`                            |
| Port           | `5432`                                 |
| Authentication | `User & Password`                      |
| User           | `paysplit`                             |
| Password       | giá trị `POSTGRES_PASSWORD` lấy ở trên |
| Database       | `paysplit`                             |

**Tab `SSH/SSL`** → tick **`Use SSH tunnel`** → `...` để tạo SSH configuration:

| Trường              | Giá trị                                            |
| ------------------- | -------------------------------------------------- |
| Host                | `161.118.233.21`                                   |
| Port                | `22`                                               |
| User name           | `ubuntu`                                           |
| Authentication type | `Key pair`                                         |
| Private key file    | đường dẫn tới `ssh-key-2026-09-04.key`             |
| Passphrase          | để trống (khoá Oracle sinh ra không có passphrase) |

Bấm **Test Connection**. Lần đầu DataGrip sẽ hỏi tải PostgreSQL driver — chọn
**Download**.

> ### ⚠️ Điểm gây nhầm lẫn số một
>
> `Host = localhost` ở tab General **không phải máy của bạn** — nó là "localhost nhìn từ
> phía VM". DataGrip mở SSH tới `161.118.233.21` trước, rồi _từ trong VM_ mới kết nối tới
> `localhost:5432`. Điền IP của VM vào ô Host ở tab General là sai và sẽ không kết nối được.

#### Cách 2 — Tunnel thủ công (dễ debug hơn khi cách 1 lỗi)

Mở một terminal riêng và **giữ nó chạy**:

```bash
ssh -i ssh-key-2026-09-04.key -N -L 55432:127.0.0.1:5432 ubuntu@161.118.233.21
```

- `-N` — không mở shell, chỉ dựng tunnel
- `-L 55432:127.0.0.1:5432` — cổng `55432` trên máy bạn ↔ cổng `5432` trên VM
- Dùng `55432` thay vì `5432` để tránh đụng PostgreSQL local (repo đang dùng `5433` cho dev)

Rồi trong DataGrip chỉ cần tab `General`, **không** cần tab SSH/SSL:

| Trường                     | Giá trị                                |
| -------------------------- | -------------------------------------- |
| Host                       | `localhost`                            |
| Port                       | `55432`                                |
| User / Password / Database | `paysplit` / `<mật khẩu>` / `paysplit` |

Kiểm tra tunnel sống chưa, trước khi mở DataGrip:

```bash
psql "postgres://paysplit:<mật khẩu>@localhost:55432/paysplit" -c "select now();"
# hoặc nếu máy không có psql:
nc -zv localhost 55432
```

#### Lưu ý khi làm việc với DB production

- Đây là **dữ liệu thật của người dùng**. Trong DataGrip nên đặt connection này ở chế độ
  `Read-only` (tab `Options` → tick `Read-only`) và gán màu cảnh báo cho data source
  (chuột phải → `Tools` → `Set Color`) để không nhầm với DB local.
- Mỗi connection DataGrip mở chiếm vài slot trong `max_connections=100`. Không nhiều,
  nhưng nhớ `Disconnect` khi xong thay vì để mở cả ngày.
- **Đừng bao giờ** sửa `"127.0.0.1:5432:5432"` thành `"5432:5432"` để "cho tiện" — xem lại
  bảng so sánh ở đầu mục này.

#### Lấy bản sao DB về máy để nghịch (không cần tunnel)

```bash
ssh -i ssh-key-2026-09-04.key ubuntu@161.118.233.21 \
  'docker compose -f ~/PaySplit-BE/deploy/docker-compose.prod.yaml exec -T postgres \
   pg_dump -U paysplit -d paysplit --no-owner -Fc' > local-copy.dump

# Nạp vào Postgres local (đang chạy ở cổng 5433 theo docker-compose.yaml của repo)
pg_restore --no-owner --no-acl -d "postgres://postgres:postgres@localhost:5433/paysplit" \
  local-copy.dump
```

Cách này an toàn nhất: bạn nghịch trên bản sao, không đụng gì tới production.

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

| Triệu chứng                                         | Nguyên nhân thường gặp                                        | Cách xử lý                                         |
| --------------------------------------------------- | ------------------------------------------------------------- | -------------------------------------------------- |
| `curl: connection timed out`                        | Firewall lớp 1 hoặc lớp 2 chưa mở                             | Làm lại **cả hai** phần ở mục 2                    |
| `permission denied ... docker.sock`                 | Chưa đăng nhập lại sau `usermod`                              | `exit` rồi SSH vào lại                             |
| Caddy không xin được cert                           | Port 80 chưa mở, hoặc `SITE_ADDRESS` không phân giải về IP VM | `dig +short 161.118.233.21.nip.io` phải ra đúng IP |
| `api` restart liên tục                              | Config validate fail (thiếu biến bắt buộc)                    | `$C logs api` — thông báo lỗi nói rõ biến nào      |
| SSE kết nối được nhưng không có event               | Thiếu `flush_interval -1` trong Caddyfile                     | Kiểm tra `deploy/Caddyfile`, `$C restart caddy`    |
| Build bị kill giữa chừng                            | Hết RAM                                                       | Thêm swap (mục 11)                                 |
| `FATAL: role "avnadmin" does not exist` khi restore | Quên `--no-owner --no-acl`                                    | Chạy lại `pg_restore` với đủ hai cờ                |
| App chạy nhưng DB trống                             | Chưa chạy migration                                           | `--profile tools run --rm migrate up`              |

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

- [ ] Commit + push thư mục `deploy/` lên nhánh `dev`
- [ ] Mở ingress 80/443 trên OCI Console
- [ ] Mở iptables 80/443 trong VM + `netfilter-persistent save`
- [ ] Cài Docker, đăng nhập lại SSH
- [ ] Clone repo (`-b dev`), `scp` file `.env.production`
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
- [ ] (tuỳ chọn) Cấu hình DataGrip qua SSH tunnel — mục 11
