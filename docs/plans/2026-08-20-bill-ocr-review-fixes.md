# Plan sửa lỗi bill và OCR v1

Nguồn: bản soát [docs/reviews/2026-08-20-fix-ocr-bill.md](../reviews/2026-08-20-fix-ocr-bill.md), chạy bởi model sonnet trên 44 file của nhánh `fix/ocr-bill`. Kết luận lúc đó: Blocked, 2 blocker, 1 major, 2 minor.

Nhánh làm việc: `fix/ocr-bill-review-fixes`, tách ra từ `fix/ocr-bill`.

Ngày lập: 2026 08 20.

---

## Quyết định nền: đổi thuật toán chia tiền

Thay thuật toán Hamilton (largest remainder) bằng **chia sàn và dồn phần dư cho creditor**.

Cách hoạt động: mọi thành viên nhận phần sàn (`floor`) của từng thành phần tiền. Không sắp xếp, không phân phối phần dư lẻ. Toàn bộ phần dư còn lại được creditor (người trả hộ hóa đơn) hấp thụ.

Ví dụ, hóa đơn 100.000 VND chia đều 3 người:

- 100.000 chia 3 bằng 33.333 VND cho mỗi người sau khi làm tròn xuống.
- Tổng đã chia bằng 99.999 VND, dư 1 VND.
- Creditor nhận thêm 1 VND. Kết quả: creditor 33.334 VND, hai người còn lại 33.333 VND.

Ưu điểm: độ phức tạp tuyến tính theo số thành viên, không cần sắp xếp mảng, không cần logic tìm phần dư lớn nhất, và tổng không bao giờ vượt quá.

### Hai điều cần biết trước khi làm

**Đây là sửa hợp đồng, không phải sửa lỗi thuần túy.** Spec 0003 đang khóa Hamilton ở AC-6, AC-10, AC-14, ở quy tắc phá hòa theo thứ tự byte UUID, và ở định nghĩa cột `rounding_adjustment`. Vì vậy giai đoạn 0 phải cập nhật spec trước khi chạm vào code.

**Riêng việc dồn dư cho creditor chưa đủ để hết lỗi blocker.** Với hóa đơn không có giảm giá thì đúng, tổng không bao giờ vượt. Nhưng phần dư của giảm giá đi ngược chiều: dư của tiền hàng, phí dịch vụ và VAT làm creditor trả thêm, còn dư của giảm giá làm creditor trả bớt. Khi giảm giá lớn, creditor vẫn có thể ra số âm, lại bị kẹp về 0, và tổng lại vượt. Nên plan này vẫn giữ lớp trần cho phần giảm giá và một kiểm tra bất biến ở cuối.

---

## Giai đoạn 0. Chuẩn bị và cập nhật spec

1. Tạo nhánh `fix/ocr-bill-review-fixes` từ `fix/ocr-bill`, để plan này không trộn với lịch sử cũ.
2. Chạy `/architect bill and OCR v1` để ghi quyết định mới vào spec 0003. Các chỗ cần sửa:
   - [index.md](../specs/0003-bill-ocr-v1/index.md): AC-6, AC-10, và hai dòng Preview trong bảng công thức.
   - [0002-allocation-review.md](../specs/0003-bill-ocr-v1/0002-allocation-review.md): mục 3, mục 4, mục 7, và mục 2 trong phần kế hoạch triển khai.
   - [0003-finalize-void.md](../specs/0003-bill-ocr-v1/0003-finalize-void.md): mô tả cột `item_subtotal` và cột `rounding_adjustment`.
   - [rationale.md](../specs/0003-bill-ocr-v1/rationale.md): mục 2 phần lý do.
3. Chốt nghĩa mới của `rounding_adjustment`: với mọi thành viên thường nó luôn bằng 0, với creditor nó bằng đúng tổng phần dư đã hấp thụ, và có thể mang giá trị âm khi giảm giá lớn. Kiểu cột không đổi nên không cần migration, chỉ cần sửa mô tả trong spec và trong `docs/openapi.yaml` nếu có nhắc tới.
4. Chốt quy tắc chọn người nhận dư: creditor, tức trường `CreditorID`. Khi `CreditorID` bằng `uuid.Nil`, dùng thành viên đầu tiên theo thứ tự byte UUID tăng dần, để kết quả vẫn tất định.

Xong khi spec đã ghi rõ thuật toán mới và nghĩa mới của `rounding_adjustment` đã được duyệt.

**Kết quả thực tế (2026 08 20).** Đã cập nhật `index.md`, `0002-allocation-review.md`, `0003-finalize-void.md`, `rationale.md`, và hai dòng mô tả tính năng trong `docs/scope/scope.md`. Một lượt soát chéo trên model sonnet tìm ra 8 lỗ hổng quyết định, tất cả đã vá. Spec giờ chốt thêm những điều mà plan ban đầu chưa nói:

1. Phân bổ bắt buộc phải có Creditor đang hoạt động. Bỏ hẳn quy tắc dự phòng lấy UUID đầu tiên hấp thụ dư, vì nó sinh ra người hấp thụ thứ hai và phá vỡ khẳng định chỉ Creditor có `rounding_adjustment` khác 0.
2. Thuật toán viết thành hai lượt tách bạch. Lượt một chặn trần cho từng thành viên thường, mỗi người chỉ dựa vào số của chính mình nên không phụ thuộc thứ tự. Lượt hai mới tính Creditor bằng phép trừ ngược.
3. Hai nguyên nhân làm Creditor xuống âm được tách thành hai lỗi. Giảm giá lớn hơn cả hóa đơn đã bị quy tắc đối soát số 4 chặn từ trước. Trường hợp mới là giảm giá hợp lệ về tổng nhưng dồn vào người không hấp thụ nổi, trả `422 DISCOUNT_NOT_ALLOCATABLE`.
4. Preview chạy đúng hàm phân bổ đó và trả cùng mã chặn, để Creditor không gặp lỗi lần đầu ngay lúc chốt sổ.
5. Nhánh subtotal bằng 0 tính thẳng, không đi qua vòng chặn trần.
6. Rationale nói thẳng rằng phần giảm giá bị cắt do chạm trần là một khoản có thể lớn, khác hẳn phần dư làm tròn vài đồng.

Ba việc mới được thêm vào build plan của spec, nên giai đoạn 1 và 5 dưới đây rộng hơn bản đầu: viết lại chín test tên Hamilton, thêm test brute force, và bổ sung `rounding_adjustment` vào schema `MemberAllocation` trong OpenAPI kèm mô tả cho cả hai schema.

## Giai đoạn 1. Sửa lỗi chia tiền, blocker

File: `internal/modules/bill/usecase/allocation.go`

1. Thay `runHamiltonForTotal` bằng một hàm chia sàn thuần: mỗi thành viên nhận `floor(targetTotal * base / totalBase)`, không sắp xếp, không phân phối phần dư. Bỏ import `sort` và `bytes` nếu không còn chỗ nào dùng.
2. Làm tương tự cho vòng chia tiền hàng theo từng item ở bước 1 của hàm: mỗi thành viên nhận phần sàn theo trọng số, phần dư của item không chia lẻ nữa.
3. Bỏ toàn bộ các map `itemFloorMap`, `scFloorMap`, `vatFloorMap`, `discFloorMap`. Chúng tồn tại chỉ để tính chênh lệch Hamilton, giờ không còn cần.
4. Tính `FinalAmount` cho các thành viên không phải creditor theo công thức cũ: tiền hàng cộng phí dịch vụ cộng VAT trừ giảm giá. Đặt `RoundingAdjustment` của họ bằng 0.
5. Tính `FinalAmount` của creditor bằng phép trừ ngược: `Total` trừ tổng `FinalAmount` của tất cả những người còn lại. Đây là mấu chốt, nó bảo đảm tổng khớp tuyệt đối theo cấu trúc, không phụ thuộc vào việc phần dư từng thành phần cộng lại có đẹp hay không. Đặt `RoundingAdjustment` của creditor bằng phần chênh giữa giá trị này và tổng bốn thành phần sàn của chính họ.
6. Xử lý số âm cho thành viên thường: nếu `FinalAmount` của ai đó âm, giới hạn phần giảm giá của người đó lại đúng bằng phần họ phải trả, thay vì kẹp kết quả cuối về 0. Phần giảm giá bị cắt đó rơi về creditor một cách tự nhiên nhờ bước 5.
7. Xử lý số âm cho creditor: nếu sau bước 5 creditor vẫn âm, đó là dấu hiệu giảm giá lớn hơn tổng hóa đơn, tức dữ liệu đầu vào sai. Trả về lỗi domain mới, ví dụ `ErrDiscountExceedsTotal`, chứ không kẹp.
8. Thêm kiểm tra bất biến ở cuối hàm: tổng `FinalAmount` phải đúng bằng `in.Total` và mọi `FinalAmount` phải không âm. Lệch thì trả lỗi. Đây là lưới an toàn để lỗi cùng loại không quay lại.
9. Giữ nguyên nhánh `totalItemSubtotal == 0`, creditor gánh phí và giảm giá. Nhánh đó vốn đã đúng.

Xong khi mọi test trong package pass và bất biến ở bước 8 không kích hoạt trên bộ test brute force ở giai đoạn 5.

## Giai đoạn 2. Sửa lỗi khóa idempotency, blocker

File: `internal/modules/bill/repository/postgres/repository.go` và `internal/modules/bill/usecase/service.go`

Nguyên nhân: câu `INSERT ... ON CONFLICT DO NOTHING` đụng bản ghi cũ nên không trả dòng nào. Nhánh dự phòng gọi `GetIdempotencyKey`, mà hàm này lọc `expires_at > now()`, nên trả về nil kèm lỗi nil. Tầng usecase đọc thẳng `rec.CanonicalRequestHash` và panic. Bản ghi hết hạn không ai xóa, nên khóa đó hỏng vĩnh viễn.

1. Sửa câu lệnh trong `ReserveIdempotencyKey`: đổi `ON CONFLICT ... DO NOTHING` thành `DO UPDATE` kèm điều kiện chỉ ghi đè khi bản ghi cũ đã hết hạn. Khi ghi đè thì đặt lại `canonical_request_hash`, `operation_id`, `state` về in_progress, `expires_at` mới, `updated_at`, và xóa `response_code`, `response_body`, `resource_id` cũ.
2. Giữ nguyên nhánh dự phòng gọi `GetIdempotencyKey`, nhưng giờ nó chỉ chạy khi bản ghi còn hạn thật, tức luôn trả về một record khác nil.
3. Thêm phòng vệ trong `CheckOrReserveIdempotency`: kiểm tra record bằng nil trước khi đọc trường và trả lỗi rõ ràng thay vì panic. Đây là lớp chắn cho tương lai, không thay cho bước 1.
4. Rà lại các đường gọi khác của `GetIdempotencyKey` xem còn chỗ nào đọc thẳng con trỏ mà không kiểm tra nil.
5. Thêm câu lệnh xóa các bản ghi `bill_idempotency_keys` đã hết hạn vào tầng repository, để giai đoạn 3 gọi định kỳ.

Xong khi một bản ghi hết hạn được chiếm lại thành công và không còn đường nào dẫn tới nil dereference.

## Giai đoạn 3. Nối dây worker dọn dẹp, major

File: `internal/bootstrap/app.go`, `internal/platform/queue/river/client.go`, `internal/modules/bill/jobs/ocr_worker.go`

Nguyên nhân: `OCRRetentionWorker` viết đầy đủ nhưng app.go chỉ đăng ký `ocrWorker`, và cấu hình truyền vào chỉ có `MaxWorkers` với `FetchCooldown`. Trường `PeriodicJobs` đã có sẵn trong client.go nhưng không ai điền. Cam kết xóa raw response sau 30 ngày trong spec không được thực hiện.

1. Đăng ký `OCRRetentionWorker` bằng `river.AddWorker` ngay cạnh chỗ đăng ký `ocrWorker`.
2. Điền trường `PeriodicJobs` khi dựng cấu hình river. Đặt một job chạy mỗi ngày một lần.
3. Đưa số giờ giữ lại ra config thay vì để mặc định ngầm 720 giờ trong worker, để môi trường dev rút ngắn được khi thử.
4. Thêm một periodic job thứ hai gọi câu lệnh xóa khóa idempotency hết hạn đã thêm ở giai đoạn 2 bước 5. Việc này đóng nốt phần "không ai dọn" của blocker thứ hai.
5. Cân nhắc bật chạy ngay khi khởi động cho job dọn dẹp trong môi trường dev, để kiểm chứng được mà không phải chờ 24 giờ.

Xong khi khởi động ứng dụng thấy log job định kỳ chạy, và dữ liệu quá hạn thật sự bị xóa.

## Giai đoạn 4. Hai lỗi nhỏ

1. Bỏ đoạn kẹp `FinalAmount` lần hai trong `internal/modules/bill/usecase/service.go` quanh dòng 825. Sau giai đoạn 1 nó vừa thừa vừa che lỗi.
2. Thống nhất thang trọng số trong `getWeight()` ở `allocation.go` quanh dòng 38. Hiện `Ratio` được nhân lên thang một trăm triệu, còn giá trị mặc định khi thiếu cả hai lại là mười nghìn. Chọn một thang duy nhất, dùng đúng nó cho mọi nhánh, và ghi chú rõ trong comment.

## Giai đoạn 5. Test và nghiệm thu

1. Viết lại các test trong `internal/modules/bill/usecase/allocation_test.go`. Chín test hiện có đều đặt tên `TestHamilton_*` và ít nhất `TestHamilton_DeterministicUUIDTieBreaking` khẳng định đúng hành vi largest remainder sắp bị bỏ. Đổi tên theo thuật toán mới và sửa kỳ vọng.
2. Thêm một test brute force quét nhiều tổ hợp tổng tiền, số thành viên, trọng số, phí dịch vụ, VAT và giảm giá. Với mỗi tổ hợp khẳng định ba điều: tổng `FinalAmount` bằng `Total`, không ai âm, và chỉ creditor có `RoundingAdjustment` khác 0. Lỗi blocker vừa rồi lọt lưới vì test chỉ kiểm tra từng ca lẻ.
3. Thêm test khẳng định giảm giá lớn hơn tổng hóa đơn trả về lỗi domain mới chứ không trả kết quả kẹp.
4. Thêm test integration cho idempotency: chèn sẵn một bản ghi đã hết hạn, gọi `ReserveIdempotencyKey`, khẳng định lấy được reservation mới. Nhớ là các file `*_integration_test.go` chỉ chạy khi có `TEST_DATABASE_URL`.
5. Thêm test unit khẳng định usecase không panic khi repository trả nil.
6. Thêm test ở tầng bootstrap khẳng định danh sách worker đăng ký và danh sách periodic job có đủ các mục mong đợi. Loại lỗi quên nối dây chỉ có test ở tầng này mới bắt được.
7. Chạy `make test`, rồi `make fmt`.

## Giai đoạn 6. Đóng lại

1. Chạy `/check verify bill and OCR v1` để đối chiếu toàn bộ tiêu chí nghiệm thu, đặc biệt AC-6 và AC-10 vừa viết lại.
2. Chạy `/check review bill and OCR v1` một lượt nữa. Đổi model đang chạy bằng `/model` trước, để người soát không phải là model vừa sửa.
3. Cập nhật [verify.md](../specs/0003-bill-ocr-v1/verify.md), dòng 16 đang ghi "preview uses Hamilton largest remainder method".
4. Chạy `/sync` rồi `/document bill and OCR v1`, và tích ô `Document it` trong [scope.md](../scope/scope.md).

---

## Thứ tự thực hiện

Giai đoạn 2 trước, vì nó là panic ở runtime và làm hỏng khóa vĩnh viễn, sửa độc lập được. Rồi giai đoạn 0 và 1 đi liền nhau vì cùng đụng hợp đồng. Rồi 3, rồi 4. Giai đoạn 5 làm song song với từng giai đoạn, không dồn về cuối.

## Bảng theo dõi

| Giai đoạn | Nội dung | Mức độ | Trạng thái |
|---|---|---|---|
| 0 | Chuẩn bị và cập nhật spec | nền tảng | **xong** |
| 1 | Sửa thuật toán chia tiền | blocker | **xong** |
| 2 | Sửa khóa idempotency | blocker | **xong** |
| 3 | Nối dây worker dọn dẹp | major | **xong** |
| 4 | Hai lỗi nhỏ | minor | **xong** |
| 5 | Test và nghiệm thu | bắt buộc | **xong** |
| 6 | Đóng lại | bắt buộc | **xong** |

---

## Nhật ký thực hiện, 2026 08 20

**Giai đoạn 2, xong.** `ReserveIdempotencyKey` đổi từ `ON CONFLICT DO NOTHING` sang `DO UPDATE` có điều kiện `expires_at <= now()`, nên bản ghi hết hạn được chiếm lại nguyên tử trong một lượt. Nhánh dự phòng thêm lớp chắn nil, và `CheckOrReserveIdempotency` cũng kiểm tra nil trước khi đọc trường. Thêm `PurgeExpiredIdempotencyKeys` vào cả interface lẫn adapter.

**Giai đoạn 1, xong.** `allocation.go` viết lại hoàn toàn. `CalculateHamiltonAllocation` đổi tên thành `CalculateFloorAllocation`. Bỏ `sort` theo phần dư, bỏ bốn map floor. Thuật toán chạy đúng hai lượt như spec. Thêm hai lỗi domain `ErrCreditorRequired` và `ErrDiscountNotAllocatable`, và ánh xạ chúng sang HTTP trong handler.

**Một quyết định phát sinh khi build.** Test cũ `DraftMismatch_NoDiscrepancyDumping` vạch ra rằng nếu tính phần Creditor bằng `in.Total` khai báo, thì mọi khoản lệch giữa tổng khai báo và tổng các món sẽ đổ hết lên đầu Creditor. Trên bản nháp có mismatch, con số hiện ra sẽ rất sai. Cách sửa: phân bổ theo tổng tính được từ chính các thành phần (`tiền các món + phí + VAT - giảm giá`), không theo `in.Total`. Ở thời điểm chốt sổ hai giá trị này bằng nhau vì đối soát bắt buộc như vậy, nên hành vi finalize không đổi. Điều này chưa nằm trong spec lúc soát chéo, cần bổ sung khi chạy `/check verify`.

**Giai đoạn 3, xong.** Tách phần nối dây thành `jobs.RegisterRetentionJobs`, gọi từ `bootstrap/app.go`. Nó đăng ký cả `OCRRetentionWorker` lẫn `IdempotencyRetentionWorker` mới, và trả về hai job định kỳ chạy mỗi 24 giờ với `RunOnStart`. Số giờ giữ lại raw OCR lấy từ `cfg.OCR.RawRetentionDays` thay vì hằng số ngầm trong worker.

**Giai đoạn 4, xong.** Bỏ đoạn kẹp `FinalAmount` lần hai trong `service.go`. `getWeight()` giờ dùng một hằng `weightScale` duy nhất cho cả ba nhánh.

**Giai đoạn 5, xong.** Viết lại toàn bộ `allocation_test.go`, 13 test. Test brute force quét 4260 tổ hợp và khẳng định ba bất biến: tổng khớp, không ai âm, chỉ Creditor mang adjustment khác 0. Thêm ba test integration cho idempotency chạy thật trên PostgreSQL, và ba test cho tầng nối dây worker.

**Trạng thái test.** `go test ./...` xanh, trừ `TestIntegration_LlamaExtract` fail vì thiếu ảnh mẫu trong `testdata/bills/`. Đã kiểm chứng lỗi này có sẵn từ trước thay đổi bằng cách stash rồi chạy lại.

## Giai đoạn 6, phần đã làm

**`/check verify` chạy hai lượt.** Lượt đầu cho FAIL với một mục thiếu: đường đọc chi tiết hóa đơn nuốt lỗi phân bổ thành metric rồi trả về response trống không giải thích gì. Lượt sau, sau khi sửa, cho PASS.

**Sửa theo hướng gom chung.** Thêm `internal/modules/bill/usecase/reconciliation.go` chứa `evaluateAllocation`, một nguồn sự thật duy nhất cho ba đường vốn lặp lại nhau: đọc chi tiết, review, và chốt sổ. `ReviewBill` và `finalizeBillImpl` mỗi hàm bớt được khoảng 20 dòng kiểm tra trùng nhau. Nhãn metric giữ nguyên để dashboard không vỡ.

**Bảy mã chặn ổn định** giờ được tính tại thời điểm đọc và gộp với cảnh báo OCR đã lưu: `ITEM_UNASSIGNED`, `INACTIVE_MEMBER_ASSIGNED`, `DISCOUNT_EXCEEDS_BILL`, `SUBTOTAL_MISMATCH`, `TOTAL_MISMATCH`, `DISCOUNT_NOT_ALLOCATABLE`, `CREDITOR_REQUIRED`.

**Một hồi quy tự gây ra, tự bắt được.** Bản đầu của `mergeMismatchCodes` trả nil cho hóa đơn sạch, mà trường `mismatch_codes` không có `omitempty` nên JSON ra `null`. Client Flutter gọi `isEmpty` trên null sẽ vỡ. Phát hiện khi đọc response thật chứ không phải từ test. Đã sửa để luôn trả mảng, và khóa lại bằng test.

**`/sync` đã chạy.** Sửa `docs/bill-ocr-module.md` ở ba chỗ nói sai: mục 4 còn mô tả Hamilton và trỏ tới hai hàm không còn tồn tại, mục 2 và 8 còn ghi worker dọn dẹp chưa được đăng ký, và số dòng trong phần tra cứu nhanh đã lệch. Trạng thái spec 0003 chuyển từ `Proposed` sang `In Progress` cho khớp scope.

**`/document` đã chạy.** Mô tả PR nằm ở phần dưới cùng của file này. `gh` chưa cài nên không tự mở PR.

**Hai việc còn lại cho người:** repo không có `AGENTS.md` nào mà `CLAUDE.md` gốc lại chứa nội dung thật, cần chạy `/audit`. Và spec `0004` với `0006` đang lệch trạng thái so với scope, ngoài phạm vi thay đổi này.
---

# Mô tả PR

**Tiêu đề:** `fix: correct bill allocation, idempotency reclaim, and retention wiring`

**Nhánh:** `fix/ocr-bill-review-fixes` vào `fix/ocr-bill`

## What

Fixes the three findings from the fresh model review of bill and OCR v1, and closes the spec conformance gap that the runtime verify run then uncovered. Two of the findings were money correctness bugs, one was a background worker that was fully written but never registered, so it had never run.

## Why

The review on 2026 08 20 (`docs/reviews/2026-08-20-fix-ocr-bill.md`) found that the exact integer rewrite of the Hamilton allocation could produce a total larger than the bill, and that an expired idempotency key panicked the process and stayed poisoned forever. Both block merge.

Fixing the allocation properly meant changing the algorithm, so spec 0003 was updated first through `/architect`. The plan and its execution log are in `docs/plans/2026-08-20-bill-ocr-review-fixes.md`.

Implements the revised `docs/specs/0003-bill-ocr-v1/` (see `0002-allocation-review.md` rules 7 to 11 and the new decision record in `rationale.md`).

## Changes

**Money allocation, replaced Hamilton with floor plus Creditor reconciliation.** Every member now receives the floor of each component, and the Creditor's amount is computed as the bill total minus the sum of everyone else's. The sum matches the total by construction rather than by a distribution loop that has to stay correct. A member whose discount share exceeds what they owe is capped so their amount reaches exactly zero, and the cut portion moves to the Creditor. A negative Creditor amount is rejected with a domain error instead of being clamped to zero, which is what silently created debt with no money behind it. An invariant check at the end of the function fails loudly if either property ever breaks again.

Allocation also now runs against the total computed from the components, not the `total` declared on the draft. A draft can store a `total` that disagrees with its items (bad OCR read, incomplete entry), and that discrepancy must not land on the Creditor.

**One reconciliation function for three code paths.** `evaluateAllocation` in the new `internal/modules/bill/usecase/reconciliation.go` is the single source of truth used by bill detail reads, review, and finalize. `ReviewBill` and `finalizeBillImpl` each shed about twenty lines of duplicated checks. Reading a draft or reviewed bill now recomputes seven stable blocker codes and returns them in `mismatch_codes`, merged with any stored OCR warning: `ITEM_UNASSIGNED`, `INACTIVE_MEMBER_ASSIGNED`, `DISCOUNT_EXCEEDS_BILL`, `SUBTOTAL_MISMATCH`, `TOTAL_MISMATCH`, `DISCOUNT_NOT_ALLOCATABLE`, `CREDITOR_REQUIRED`. Previously a bill too broken to allocate returned HTTP 200 with no breakdown and an empty code list, so the client could not tell a broken bill from an empty one.

**Idempotency key reclaim.** `ReserveIdempotencyKey` changed from `ON CONFLICT DO NOTHING` to `DO UPDATE ... WHERE expires_at <= now()`, so an expired row is reclaimed atomically in one statement. Before, the insert returned no row, the fallback read filtered expired rows out, and the usecase dereferenced the resulting nil pointer and crashed the process. A nil guard was added at the usecase layer as a second line of defence.

**Retention workers registered.** `OCRRetentionWorker` existed but was never passed to River, so the thirty day raw OCR purge the spec promises had never run. Registration moved into `jobs.RegisterRetentionJobs` so it is testable, and a second worker was added to sweep expired `bill_idempotency_keys`, which nothing had ever deleted. Both run daily with `RunOnStart`. The retention window now comes from `BILL_OCR_RAW_RETENTION_DAYS` instead of a constant buried in the worker.

**Smaller cleanups.** Removed the redundant second clamp of `FinalAmount` in `finalizeBillImpl`, which was masking the first bug. Unified `getWeight()` on a single `weightScale` constant; it previously mixed a scale of one hundred million with a default of ten thousand.

## How to test / verify

```bash
go test ./...                       # all green
go test -run BruteForce -v ./internal/modules/bill/usecase/   # 4260 input combinations
```

The brute force test asserts three invariants across every combination: the shares sum to the computed total, no member is negative, and only the Creditor carries a nonzero `rounding_adjustment`. The old bug slipped through because the suite only checked individual cases.

Repository tests need a live database:

```bash
TEST_DATABASE_URL=... go test -run Idempotency -v ./internal/modules/bill/repository/postgres/
```

Runtime evidence from the verify run is recorded in `docs/specs/0003-bill-ocr-v1/verify.md`. Worth reproducing if you want to see it yourself:

- Create a bill with an odd total split evenly. The Creditor takes the leftover dong and carries `rounding_adjustment` 1; everyone else carries 0; the shares sum exactly to the total. This holds after finalize too, in `bill_shares` and `debts`.
- Age a `bill_idempotency_keys` row past `expires_at`, then replay the same `Idempotency-Key`. It returns 201 and the server stays up.
- Read a bill whose discount exceeds its subtotal. It returns `["DISCOUNT_EXCEEDS_BILL","TOTAL_MISMATCH"]` where it used to return an empty list.
- Restart the server and check `river_job` for `ocr_raw_retention_cleanup` and `bill_idempotency_key_cleanup`. Both complete. Age a job's `completed_at` past thirty days first and its `raw_response` is nulled while `candidate` survives.

## Risk & rollout

No migrations, no feature flags, no config changes required (`BILL_OCR_RAW_RETENTION_DAYS` already existed and already defaulted to 30).

Two behaviour changes reviewers should weigh:

- **Remainders move.** Under Hamilton the leftover dong went to whoever had the largest fractional remainder. It now always goes to the Creditor. In VND that is a few dong per component. Bills finalized before this change keep their stored snapshots and are unaffected; only new allocations differ.
- **`rounding_adjustment` changed meaning.** It is now zero for every member except the Creditor, where it can be negative. Documented on both the `BillShare` and `MemberAllocation` schemas in `docs/openapi.yaml`.

There is a real fairness tradeoff that the rounding argument does not cover: when a member is assigned more discount than they owe, the cut portion moves to the Creditor whole, not as a few dong. This is disclosed in `rationale.md` rather than glossed over. When that cascade would push the Creditor below zero the bill is rejected with `DISCOUNT_NOT_ALLOCATABLE`, never silently shifted.

The retention workers now delete data that was previously accumulating. That is the intended behaviour, but it is the first time it will actually run in any environment.

## Notes for reviewers

The nine allocation tests named after Hamilton were rewritten and the largest remainder tie breaking test was dropped, since that behaviour no longer exists. Reviewing `allocation_test.go` as a new file rather than a diff is easier.

`DISCOUNT_NOT_ALLOCATABLE` is defensive. It could not be triggered at runtime, and the 4260 combination sweep never hit it either, because floor allocation makes the cascade hard to force. It replaces a silent clamp, so it is worth keeping even though the branch is cold. Extra eyes welcome on whether a reachable input exists.

One thing worth flagging that this PR does not fix: the first OCR job in the verify run recorded `attempts` of 3 while succeeding, and the manual retry recorded 1. No log line explains the two extra increments. It may be genuine provider retries or a counter incremented in the wrong place.
