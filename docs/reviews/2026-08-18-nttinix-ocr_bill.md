# Báo Cáo Đánh Giá Mã Nguồn: nttinix/ocr_bill (18-08-2026)

**Người đánh giá**: Senior Reviewer (Antigravity Code Review)  
**Phạm vi**: 300 tệp tin thay đổi, nhánh `nttinix/ocr_bill` so với `main`  
**Kết luận**: Phê duyệt có góp ý nhỏ (Approve with nits)  

---

## 1. Tóm tắt tổng quan
Việc triển khai Đặc tả 3 (Bill & OCR v1) cung cấp một hệ thống bóc tách hóa đơn tự động và phân bổ tài chính có độ tin cậy và hiệu năng cao. Hệ thống tích hợp mượt mà giữa các giao dịch cơ sở dữ liệu PostgreSQL, hàng đợi River Queue xử lý tác vụ nền, chuẩn hóa bóc tách AI qua LlamaExtract, lưu trữ ảnh riêng tư trên Cloudinary, luồng phát sự kiện trực tiếp Server-Sent Events (SSE), và giải thuật phân bổ công bằng Hamilton Largest Remainder. Toàn bộ mã nguồn đáp ứng đầy đủ 14 Tiêu chuẩn nghiệm thu (Acceptance Criteria), các bài kiểm thử tích hợp và đơn vị đều vượt qua 100%.

---

## 2. Các điểm mạnh nổi bật
- **Độ chính xác tài chính tuyệt đối**: Giải thuật phân bổ Hamilton (`allocation.go`) loại bỏ hoàn toàn sai lệch làm tròn số tiền lẻ VND, đảm bảo tổng số tiền phân bổ cho từng thành viên khớp chính xác từng đồng với tổng hóa đơn ($\sum \text{final\_amount} = \text{bill.Total}$), đồng thời sử dụng thứ tự 16 byte UUID chuẩn để xử lý hòa một cách nhất quán.
- **Bảo vệ chống xung đột đồng thời**: Cơ chế khóa dòng PostgreSQL (`SELECT ... FOR UPDATE`) kết hợp Optimistic Locking (`version`) ngăn chặn hoàn toàn tình trạng ghi đè dữ liệu hoặc thực thi trùng lặp các tác vụ OCR.
- **Xử lý bóc tách AI linh hoạt**: Bộ chuẩn hóa LlamaExtract xử lý mượt mà các định dạng số tiền tiếng Việt (ví dụ `50k`, `1.250.000đ`), ngày tháng viết tắt hoặc không rõ ràng, và tự động thử lại khi gặp sự cố mạng TLS.
- **Kiến trúc phát sự kiện trực tiếp (SSE)**: Hub quản lý kết nối (`sse_hub.go`) và trình xử lý HTTP streaming (`sse_handler.go`) gửi ngay bản chụp trạng thái hiện tại (snapshot), gửi tín hiệu giữ kết nối (ping) mỗi 15 giây, và tự động đóng sạch sau 15 phút để ứng dụng di động kết nối lại an toàn.

---

## 3. Lỗi nghiêm trọng chặn phát hành (Blockers)
*Không có.*

---

## 4. Vấn đề lớn cần lưu ý (Major)
*Không có.*

---

## 5. Góp ý cải tiến nhỏ (Minor)
### 🟢 Tối ưu hóa truy vấn đếm số lần quét lại OCR thủ công (Đã hoàn thành)
- **Vị trí**: [`internal/modules/bill/repository/postgres/queries/bill.sql:254`](file:///home/vsf-tinnt32-u/Documents/PaySplit-BE/internal/modules/bill/repository/postgres/queries/bill.sql#L254)
- **Hiện trạng**: Truy vấn `CountManualOCRAttemptsInWindow` sử dụng điều kiện `bill_id = $1 AND created_at >= $2`.
- **Đã xử lý**: Đã bổ sung chỉ mục `CREATE INDEX idx_ocr_jobs_bill_created ON ocr_jobs(bill_id, created_at DESC)` vào migration `000004_bill_and_ocr_v1.sql` và đã áp dụng thành công lên cơ sở dữ liệu PostgreSQL.

---

## 6. Góp ý phong cách mã nguồn (Nits)
- ⚪ [`internal/modules/bill/delivery/http/handler.go:403`](file:///home/vsf-tinnt32-u/Documents/PaySplit-BE/internal/modules/bill/delivery/http/handler.go#L403): Hàm `writeDomainError` ánh xạ chuẩn xác toàn bộ lỗi nghiệp vụ sang các mã lỗi API công khai và mã trạng thái HTTP tương ứng.
- ⚪ [`internal/modules/bill/jobs/ocr_worker.go:120`](file:///home/vsf-tinnt32-u/Documents/PaySplit-BE/internal/modules/bill/jobs/ocr_worker.go#L120): Bộ nhớ đệm tải ảnh tạm thời từ Cloudinary được giải phóng kịp thời sau khi quá trình bóc tách hoàn tất.
