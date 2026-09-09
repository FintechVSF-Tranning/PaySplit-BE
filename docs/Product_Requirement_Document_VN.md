![][image1]

# **PAYSPLIT - HỆ THỐNG CHIA TIỀN HÓA ĐƠN THÔNG MINH**

## **Tài liệu Yêu cầu Sản phẩm (Product Requirement Document)**

| Đội ngũ PaySplit | |
| ----------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Thành viên nhóm** | Phạm Lê Hoàng Nam, Phạm Thanh Lam, Nguyễn Trọng Tín |
| **Mentor** | Trần Quang Hiển (VSF-FINTECH-VDTDVTC) |
| **Ext Mentor** | Bành Quốc Danh (VSF-FINTECH&TT-PTPM), Phan Công Huân (VSF-FINTECH-VDTDVTC), Nguyễn Mạnh Tể (VSF-FINTECH&TT-PTPM), Nguyễn Nam Trường (VSF-FINTECH-VDTDVTC) |

<p align="center">– Hà Nội, Tháng 09/2026 –</p>

---

## **Mục lục**

- [**I. Lịch sử Thay đổi**](#i-lịch-sử-thay-đổi)
- [**II. Tài liệu Yêu cầu Sản phẩm**](#ii-tài-liệu-yêu-cầu-sản-phẩm)
  - [**1. Giới thiệu Sản phẩm**](#1-giới-thiệu-sản-phẩm)
    - [1.1 Tóm tắt Tổng quan (Executive Summary)](#11-tóm-tắt-tổng-quan-executive-summary)
    - [1.2 Bối cảnh & Bài toán Cần giải quyết](#12-bối-cảnh--bài-toán-cần-giải-quyết)
      - [1.2.1 Bối cảnh Chi tiêu Nhóm](#121-bối-cảnh-chi-tiêu-nhóm)
      - [1.2.2 Bối cảnh Điều phối Thanh toán của PaySplit](#122-bối-cảnh-điều-phối-thanh-toán-của-paysplit)
      - [1.2.3 Thách thức Kỹ thuật](#123-thách-thức-kỹ-thuật)
      - [1.2.4 Giá trị của Nguyên mẫu](#124-giá-trị-của-nguyên-mẫu)
      - [1.2.5 Phạm vi & Mục tiêu](#125-phạm-vi--mục-tiêu)
  - [**2. Tổng quan Sản phẩm**](#2-tổng-quan-sản-phẩm)
  - [**3. Yêu cầu Người dùng**](#3-yêu-cầu-người-dùng)
    - [3.1 Các Tác nhân (Actors)](#31-các-tác-nhân-actors)
    - [3.2 Use Cases](#32-use-cases)
      - [3.2.1 Sơ đồ Use Case](#321-sơ-đồ-use-case)
      - [3.2.2 Mô tả Use Case](#322-mô-tả-use-case)
  - [**4. Yêu cầu Chức năng**](#4-yêu-cầu-chức-năng)
    - [4.1 Các Tính năng Cốt lõi của Hệ thống](#41-các-tính-năng-cốt-lõi-của-hệ-thống)
      - [4.1.1 - Đăng nhập (Sign In)](#411---đăng-nhập-sign-in)
      - [4.1.2 - Đăng ký (Sign Up)](#412---đăng-ký-sign-up)
      - [4.1.3 - Xác thực Email (Verify Email)](#413---xác-thực-email-verify-email)
      - [4.1.4 - Gửi lại OTP Xác thực (Resend Verification OTP)](#414---gửi-lại-otp-xác-thực-resend-verification-otp)
      - [4.1.5 - Làm mới Token & Xoay vòng Phiên (Token Refresh & Session Rotation)](#415---làm-mới-token--xoay-vòng-phiên-token-refresh--session-rotation)
      - [4.1.6 - Quên mật khẩu & Đặt lại mật khẩu (Forgot & Reset Password)](#416---quên-mật-khẩu--đặt-lại-mật-khẩu-forgot--reset-password)
      - [4.1.7 - Đăng xuất (Sign Out)](#417---đăng-xuất-sign-out)
      - [4.1.8 - Đổi mật khẩu (Change Password)](#418---đổi-mật-khẩu-change-password)
      - [4.1.9 - Cập nhật Hồ sơ & Thông tin Ngân hàng (Update Profile & Bank Info)](#419---cập-nhật-hồ-sơ--thông-tin-ngân-hàng-update-profile--bank-info)
      - [4.1.10 - Tạo Nhóm Mới (Create New Group)](#4110---tạo-nhóm-mới-create-new-group)
      - [4.1.11 - Tạo Liên kết Mời vào Nhóm (Generate Group Invite)](#4111---tạo-liên-kết-mời-vào-nhóm-generate-group-invite)
      - [4.1.12 - Xem trước & Tham gia Nhóm (Preview & Join Group)](#4112---xem-trước--tham-gia-nhóm-preview--join-group)
      - [4.1.13 - Xóa Thành viên & Rời Nhóm (Remove Member & Leave Group)](#4113---xóa-thành-viên--rời-nhóm-remove-member--leave-group)
      - [4.1.14 - Chuyển giao Vai trò Trưởng nhóm (Transfer Captain Role)](#4114---chuyển-giao-vai-trò-trưởng-nhóm-transfer-captain-role)
      - [4.1.15 - Giải tán (Lưu trữ) Nhóm (Disband / Archive Group)](#4115---giải-tán-lưu-trữ-nhóm-disband--archive-group)
      - [4.1.16 - Khóa và Mở khóa Nộp Hóa đơn (Lock / Unlock Bill Submissions)](#4116---khóa-và-mở-khóa-nộp-hóa-đơn-lock--unlock-bill-submissions)
      - [4.1.17 - Tải ảnh Hóa đơn & Trích xuất OCR Bất đồng bộ (Upload Bill & Async OCR)](#4117---tải-ảnh-hóa-đơn--trích-xuất-ocr-bất-đồng-bộ-upload-bill--async-ocr)
      - [4.1.18 - Cập nhật Hóa đơn Nháp & Phân bổ Mục (Update Draft Bill & Assignments)](#4118---cập-nhật-hóa-đơn-nháp--phân-bổ-mục-update-draft-bill--assignments)
      - [4.1.19 - Kiểm tra Hóa đơn (Review Bill)](#4119---kiểm-tra-hóa-đơn-review-bill)
      - [4.1.20 - Chốt Hóa đơn (Finalize Bill)](#4120---chốt-hóa-đơn-finalize-bill)
      - [4.1.21 - Chốt Hàng loạt Tất cả Hóa đơn (Batch Finalize-All Bills)](#4121---chốt-hàng-loạt-tất-cả-hóa-đơn-batch-finalize-all-bills)
      - [4.1.22 - Hủy bỏ Hóa đơn Đã chốt (Void Finalized Bill)](#4122---hủy-bỏ-hóa-đơn-đã-chốt-void-finalized-bill)
      - [4.1.23 - Xem Phân bổ Chi phí & Chi tiết Nhóm (View Allocated Expense & Breakdown)](#4123---xem-phân-bổ-chi-phí--chi-tiết-nhóm-view-allocated-expense--breakdown)
      - [4.1.24 - Tạo VietQR Thanh toán (Generate Payment QR)](#4124---tạo-vietqr-thanh-toán-generate-payment-qr)
      - [4.1.25 - Nộp Bằng chứng Chuyển khoản (Submit Payment Proof)](#4125---nộp-bằng-chứng-chuyển-khoản-submit-payment-proof)
      - [4.1.26 - Xác nhận hoặc Từ chối Thanh toán (Confirm / Reject Payment)](#4126---xác-nhận-hoặc-từ-chối-thanh-toán-confirm--reject-payment)
      - [4.1.27 - Nhắc nợ Thủ công & Trình quét Tự động (Debt Reminders & Auto Scan)](#4127---nhắc-nợ-thủ-công--trình-quét-tự-động-debt-reminders--auto-scan)
      - [4.1.28 - Thông báo Trong ứng dụng & Push Notification (In-App & Push Notifications)](#4128---thông-báo-trong-ứng-dụng--push-notification-in-app--push-notifications)
      - [4.1.29 - Quản trị Hệ thống & Cổng Web Admin (Admin Management & Web Portal)](#4129---quản-trị-hệ-thống--cổng-web-admin-admin-management--web-portal)
      - [4.1.30 - Kiến trúc Realtime Sự kiện Hợp nhất (Unified Realtime Architecture)](#4130---kiến-trúc-realtime-sự-kiện-hợp-nhất-unified-realtime-architecture)
      - [4.1.31 - Bắt kịp Đồng bộ Delta Nhóm (Group Delta Catch-up Synchronization)](#4131---bắt-kịp-đồng-bộ-delta-nhóm-group-delta-catch-up-synchronization)
    - [4.2 Các Chức năng Tự động của Hệ thống](#42-các-chức-năng-tự-động-của-hệ-thống)
      - [4.2.1 - Tác vụ Ngầm Tự động & Hàng đợi River Queue Worker](#421---tác-vụ-ngầm-tự-động--hàng-đợi-river-queue-worker)
  - [**5. Yêu cầu Phi chức năng**](#5-yêu-cầu-phi-chức-năng)
    - [5.1 Giao diện Bên ngoài](#51-giao-diện-bên-ngoài)
      - [5.1.1 Giao diện Người dùng (User Interface)](#511-giao-diện-người-dùng-user-interface)
      - [5.1.2 Giao diện Phần mềm (Software Interface)](#512-giao-diện-phần-mềm-software-interface)
      - [5.1.3 Giao diện Phần cứng (Hardware Interface)](#513-giao-diện-phần-cứng-hardware-interface)
    - [5.2 Thuộc tính Chất lượng](#52-thuộc-tính-chất-lượng)
      - [5.2.1 Hiệu năng & Khả năng Mở rộng (Performance & Scalability)](#521-hiệu-năng--khả-năng-mở-rộng-performance--scalability)
      - [5.2.2 Độ tin cậy & Tính Bền vững (Reliability & Robustness)](#522-độ-tin-cậy--tính-bền-vững-reliability--robustness)
      - [5.2.3 Bảo mật & Quyền riêng tư (Security & Privacy)](#523-bảo-mật--quyền-riêng-tư-security--privacy)
      - [5.2.4 Tính Giải thích được (Explainability)](#524-tính-giải-thích-được-explainability)
      - [5.2.5 Khả năng Bảo trì & Tái lập (Maintainability & Reproducibility)](#525-khả-năng-bảo-trì--tái-lập-maintainability--reproducibility)
  - [**6. Tổng quan Kiến trúc (Cấp cao)**](#6-tổng-quan-kiến-trúc-cấp-cao)
    - [6.1 Các Thành phần Hệ thống](#61-các-thành-phần-hệ-thống)
    - [6.2 Tech Stack](#62-tech-stack)
  - [**7. Cột mốc & Lộ trình Phát triển**](#7-cột-mốc--lộ-trình-phát-triển)
  - [**8. Rủi ro & Giải pháp Giảm thiểu**](#8-rủi-ro--giải-pháp-giảm-thiểu)

---

## **Danh mục Bảng**

- [Bảng 1. Lịch sử Thay đổi](#bảng-1-lịch-sử-thay-đổi)
- [Bảng 2. Tất cả Tác nhân trong Hệ thống](#bảng-2-tất-cả-tác-nhân-trong-hệ-thống)
- [Bảng 3. Bảng Mô tả Use Case](#bảng-3-bảng-mô-tả-use-case)
- [Bảng 4. Tech Stack](#bảng-4-tech-stack)
- [Bảng 5. Cột mốc & Lộ trình](#bảng-5-cột-mốc--lộ-trình)
- [Bảng 6. Rủi ro & Giải pháp Giảm thiểu](#bảng-6-rủi-ro--giải-pháp-giảm-thiểu)

---

## **Danh mục Hình ảnh**

- [Hình 1. Chu trình Chia tiền Chi tiêu Truyền thống](#hình-1-chu-trình-chia-tiền-chi-tiêu-truyền-thống)
- [Hình 2. Giá trị Cốt lõi và Lợi ích của Nguyên mẫu PaySplit](#hình-2-giá-trị-cốt-lõi-và-lợi-ích-của-nguyên-mẫu-paysplit)
- [Hình 3. Sơ đồ Bối cảnh Hệ thống](#hình-3-sơ-đồ-bối-cảnh-hệ-thống)
- [Hình 4. Sơ đồ Use Case PaySplit](#hình-4-sơ-đồ-use-case-paysplit)
- [Hình 5. Sơ đồ Use Case Xác thực PaySplit](#hình-5-sơ-đồ-use-case-xác-thực-paysplit)

---

# **I. Lịch sử Thay đổi** {#i-lịch-sử-thay-đổi}

\*A - Thêm mới (Added) M - Sửa đổi (Modified) D - Xóa bỏ (Deleted)

| Ngày | A\*, M, D | Người phụ trách | Mô tả Thay đổi |
| :---: | :---: | :--- | :--- |
| 10/08/2026 | A | NamPLH | Khởi tạo tài liệu cơ sở ban đầu. |
| 10/08/2026 | A | Toàn bộ thành viên | Bổ sung Tổng quan Sản phẩm, Chân dung Người dùng (Personas), Use cases. |
| 11/08/2026 | M | Toàn bộ thành viên | Hoàn thiện phiên bản tài liệu ban đầu. |
| 04/09/2026 | M | Toàn bộ thành viên | Cập nhật toàn diện khớp 100% với codebase thực tế của Backend Go và Frontend Flutter: xác thực đơn phiên (single-session), xử lý nền River Queue, pipeline OCR LlamaExtract bất đồng bộ (HTTP 202), thuật toán chia tiền Hamilton dùng `big.Rat`, luồng thanh toán VietQR không khóa nợ, nộp bằng chứng chuyển khoản 2 pha, thông báo đẩy FCM & in-app, cổng Web Admin nhúng, và kiến trúc realtime tối ưu kết nối. |
| 05/09/2026 | M | Toàn bộ thành viên | Bổ sung đồng bộ bắt kịp delta nhóm (`GET /api/v1/groups/{id}/sync`), đánh số thứ tự phiên bản tăng đơn điệu `roster_version`, và làm rõ ranh giới phát hành V1 Production so với đề xuất V2 (Đồng thuận Hóa đơn Con nợ tại Spec 0007). |

##### **Bảng 1. Lịch sử Thay đổi** {#bảng-1-lịch-sử-thay-đổi}

---

# **II. Tài liệu Yêu cầu Sản phẩm** {#ii-tài-liệu-yêu-cầu-sản-phẩm}

## **1. Giới thiệu Sản phẩm** {#1-giới-thiệu-sản-phẩm}

### **1.1. Tóm tắt Tổng quan (Executive Summary)** {#11-tóm-tắt-tổng-quan-executive-summary}

Tài liệu này xác định các yêu cầu sản phẩm toàn diện cho **PaySplit**, nền tảng chia tiền hóa đơn nhóm và quyết toán thông minh, được thiết kế cho các giao dịch thanh toán số ngang hàng (peer-to-peer) trực tiếp.  
**PaySplit** cho phép người dùng tạo các nhóm chi tiêu cộng tác, quét và trích xuất hóa đơn giấy bằng OCR (Vision LLM), phân bổ từng món ăn/dịch vụ với tỷ trọng linh hoạt hoặc chia đều, tính toán chính xác phần tiền của từng người tham gia thông qua số học số nguyên nghiêm ngặt kết hợp làm tròn số dư lớn nhất (thuật toán Hamilton), và tạo mã **VietQR** động riêng biệt chứa sẵn chi tiết tài khoản ngân hàng và nội dung chuyển khoản.

Các khoản thanh toán được chuyển trực tiếp giữa tài khoản ngân hàng của các thành viên thông qua ứng dụng ngân hàng số hoặc ví điện tử bên ngoài (NAPAS 247). **PaySplit hoạt động thuần túy như một dịch vụ điều phối thanh toán; hệ thống không giữ tiền của người dùng, không đóng vai trò trung gian tài chính và không thực hiện trích nợ tự động**, tuân thủ đầy đủ Nghị định 52/2024/NĐ-CP. Chủ nợ (Creditor) sẽ kiểm tra và xác nhận hoặc từ chối giao dịch thủ công sau khi xem xét bằng chứng chuyển khoản đính kèm.

Hệ thống nguyên mẫu sẵn sàng triển khai thực tế bao gồm:
- Hệ thống **Go RESTful API** hiệu năng cao và **River Queue Worker** chạy trên nền PostgreSQL 18.
- Ứng dụng di động đa nền tảng **Flutter** (iOS & Android) xây dựng theo kiến trúc Clean Architecture, Module hóa Feature-First, Riverpod và GoRouter.
- Cổng quản trị web nhúng **Web Admin Portal** dùng để giám sát thời gian thực và quản lý tài khoản.
- Động cơ sự kiện thời gian thực hợp nhất **Unified Realtime Event Engine** sử dụng Server-Sent Events (SSE) tối ưu kết nối, vận hành qua một kết nối `LISTEN/NOTIFY` PostgreSQL dùng chung duy nhất trên mỗi phiên bản máy chủ.

### **1.2. Bối cảnh & Bài toán Cần giải quyết** {#12-bối-cảnh--bài-toán-cần-giải-quyết}

#### **_1.2.1 Bối cảnh Chi tiêu Nhóm_** {#121-bối-cảnh-chi-tiêu-nhóm}

Các hoạt động nhóm như ăn uống, du lịch, thuê nhà chung hoặc sự kiện giao lưu thường phát sinh nhiều khoản thanh toán trước do các thành viên khác nhau chi trả. Hiện nay, việc tổ chức và quyết toán các khoản chi tiêu chung này thường dựa vào tin nhắn chat rời rạc, bảng tính Excel hoặc ghi chú thủ công, buộc trưởng nhóm và người trả tiền trước phải:
- Ghi chép thủ công từng hóa đơn và theo dõi ai đã trả tiền trước.
- Xác định thành viên nào đã sử dụng món cụ thể so với các chi phí dùng chung của cả nhóm.
- Phân bổ phí dịch vụ, thuế VAT và các khoản giảm giá một cách chính xác mà không gây lệch số làm tròn.
- Tính toán số tiền chính xác từng người phải trả và gửi thông tin tài khoản ngân hàng cùng số tiền cho từng người.
- Theo dõi thông báo biến động số dư ngân hàng và đối soát khoản tiền nhận được với từng hóa đơn cụ thể.
- Gửi tin nhắn nhắc nợ lặp đi lặp lại một cách ngại ngùng tới các thành viên chưa thanh toán.

###### **![][image2]**

###### **Hình 1. Chu trình Chia tiền Chi tiêu Truyền thống** {#hình-1-chu-trình-chia-tiền-chi-tiêu-truyền-thống}

Việc quản lý chi tiêu chung thủ công dễ dẫn đến sai sót, tốn nhiều công sức, gây bất tiện trong giao tiếp tài chính và làm chậm trễ quá trình thanh toán. **PaySplit** thay thế quy trình rời rạc này bằng một quy trình làm việc tự động, minh bạch và có thể kiểm tra đối soát từ đầu đến cuối.

#### **_1.2.2 Bối cảnh Điều phối Thanh toán của PaySplit_** {#122-bối-cảnh-điều-phối-thanh-toán-của-paysplit}

**PaySplit** hoạt động thuần túy với tư cách là **đơn vị điều phối chi tiêu và tạo điều kiện thanh toán**. Hệ thống không lưu giữ tiền của người dùng, không cung cấp số dư ví điện tử và không thực hiện trích nợ tài khoản tự động. Thay vào đó, khi một hóa đơn được chốt, PaySplit:
- Tính toán chính xác phần tiền của từng cá nhân tới từng 1 VNĐ.
- Tạo mã VietQR chuẩn chứa mã ngân hàng của chủ nợ, số tài khoản ngân hàng, số tiền VNĐ chính xác và mã tham chiếu duy nhất (`PAY` + 8 ký tự Base32).
- Hỗ trợ con nợ nộp bằng chứng thanh toán (ảnh chụp màn hình chuyển khoản và ghi chú).
- Cho phép chủ nợ xác nhận hoặc từ chối giao dịch thủ công sau khi kiểm tra biến động số dư tài khoản ngân hàng của mình.

Mô hình ngang hàng trực tiếp này loại bỏ trách nhiệm giữ tiền, tránh các thủ tục giấy phép trung gian thanh toán phức tạp theo quy định của pháp luật Việt Nam (Nghị định 52/2024/NĐ-CP) và đảm bảo tính minh bạch tài chính tối đa.

#### **_1.2.3 Thách thức Kỹ thuật_** {#123-thách-thức-kỹ-thuật}

Việc xây dựng một nền tảng chia tiền hóa đơn thời gian thực mạnh mẽ đòi hỏi phải giải quyết nhiều thách thức kỹ thuật phức tạp:
- **Chuẩn hóa OCR Hóa đơn:** Xử lý các định dạng hóa đơn tiếng Việt đa dạng, điều kiện ánh sáng kém, giấy bị nhàu, các dòng thuế/giảm giá phức tạp và độ trễ của nhà cung cấp dịch vụ thông qua hàng đợi bất đồng bộ và bước kiểm tra (review gate).
- **Bất biến Toán học Nghiêm ngặt:** Đảm bảo tổng số tiền phân bổ của tất cả người tham gia và các khoản nợ tạo ra luôn bằng chính xác tổng số tiền hóa đơn ($\sum \text{shares} = \text{bill\_total}$), không làm thất thoát hay phát sinh thêm dù chỉ 1 đồng lẻ, sử dụng số hữu tỉ chính xác (`big.Rat`) và thuật toán làm tròn số dư lớn nhất Hamilton có tính tất định, không thiên vị chủ nợ.
- **Xử lý Đột biến Đồng thời & Race Conditions:** Ngăn chặn các bất thường trong tính toán phân bổ, thanh toán trùng lặp và vượt quá sức chứa nhóm thông qua khóa mức hàng (`LockActiveGroup`), khóa lạc quan kiểm tra phiên bản (`version`), và khóa chống trùng lặp tất định (`Idempotency-Key`).
- **Bảo mật Đơn phiên & Xoay vòng Phiên:** Thực thi quy tắc mỗi người dùng chỉ có duy nhất một phiên hoạt động, phát hiện tái sử dụng Refresh Token, thu hồi đa phiên ngay lập tức khi đặt lại mật khẩu, và giới hạn tần suất chống dò quét brute-force theo IP/tài khoản.
- **Truyền phát Realtime Tối ưu Kết nối:** Loại bỏ việc mở nhiều kết nối SSE trên từng màn hình bằng cách đa hợp (multiplex) toàn bộ sự kiện thời gian thực qua một luồng SSE duy nhất cho mỗi phiên (`GET /api/v1/users/me/events`), được điều phối bởi một kết nối lắng nghe PostgreSQL dùng chung.

#### **_1.2.4 Giá trị của Nguyên mẫu_** {#124-giá-trị-của-nguyên-mẫu}

Hệ thống **PaySplit** mang lại giá trị vượt trội xuyên suốt toàn bộ quy trình tài chính nhóm:
- **Hiệu quả:** Vision LLM OCR trích xuất các mục trên hóa đơn và phụ phí chỉ trong vài giây; tính toán tự động loại bỏ việc tính nhẩm thủ công.
- **Công bằng & Minh bạch Tuyệt đối:** Phân bổ chi tiết từng món ăn, phân chia phụ phí/giảm giá theo tỷ lệ và giải trình làm tròn minh bạch đảm bảo mọi thành viên trả đúng phần của mình.
- **Thanh toán Không ma sát:** Tạo mã VietQR động tức thì kèm thông tin tài khoản ngân hàng có thể sao chép và mã tham chiếu chuẩn hóa giúp loại bỏ lỗi gõ nhầm trên app ngân hàng.
- **Khả năng Kiểm toán & Kiểm soát:** Xác nhận do chủ nợ kiểm soát, sổ cái hóa đơn đã chốt bất biến, nhật ký hoạt động nhóm ghi nối tiếp (append-only) và nhắc nợ tự động giúp ngăn ngừa tranh chấp và quên nợ.

![][image3]

###### **Hình 2. Giá trị Cốt lõi và Lợi ích của Nguyên mẫu PaySplit** {#hình-2-giá-trị-cốt-lõi-và-lợi-ích-của-nguyên-mẫu-paysplit}

#### **_1.2.5 Phạm vi & Mục tiêu_** {#125-phạm-vi--mục-tiêu}

**Thuộc Phạm vi Triển khai (Khả năng của Hệ thống):**
- **Xác thực & Bảo mật:** Một phiên hoạt động duy nhất cho mỗi người dùng, JWT Access Token (15 phút) được xác thực qua trạng thái phiên database (`liveAuth`), cơ chế xoay vòng Refresh Token (7 ngày) kèm phát hiện tái sử dụng, bắt buộc đăng ký số điện thoại Việt Nam với mã OTP 6 chữ số băm SHA-256 (10 phút, quy tắc vô hiệu hóa sau 5 lần nhập sai), bảo vệ chống brute-force (5 lần sai trong 15 phút $\implies$ khóa 15 phút), quản lý hồ sơ & tài khoản ngân hàng với kiểm tra danh bạ VietQR và xử lý ảnh đại diện qua Cloudinary.
- **Quản lý Nhóm:** Nhóm tạm thời hoặc cố định tối đa 50 thành viên hoạt động, mã mời Base62 8 ký tự phân biệt chữ hoa/thường chống dò quét, quét mã QR qua camera/thư viện ảnh (`mobile_scanner` / `zxing2`), chuyển giao vai trò Trưởng nhóm nguyên tử với khóa hàng `NOWAIT`, hủy kích hoạt/tái kích hoạt thành viên mềm bảo toàn tính toàn vẹn sổ cái, khóa/mở khóa nộp hóa đơn, đánh số thứ tự nhóm tăng đơn điệu (`roster_version`), và đồng bộ bắt kịp delta (`GET /api/v1/groups/{id}/sync?since=N`).
- **Xử lý Hóa đơn & OCR:** Tải lên multipart 1–5 ảnh hóa đơn (JPEG/PNG/HEIC tối đa 10MB), xử lý OCR bất đồng bộ qua River Queue và LlamaExtract trả về HTTP 202 Accepted, nhập hóa đơn nháp thủ công, phân bổ món với trọng số tùy chỉnh hoặc chia đều (`big.Rat`), khóa lạc quan qua `version`, kiểm tra điều kiện chốt thử nghiệm (các lỗi chặn `BILL_NOT_READY`), chốt hóa đơn bất biến tính toán phần tiền chính xác qua thuật toán Hamilton, hủy hóa đơn có kiểm soát trạng thái thanh toán, và xử lý chốt hàng loạt tất cả hóa đơn.
- **Quyết toán & VietQR:** Tạo VietQR không khóa nợ (chuẩn TLV/compact, mã tham chiếu duy nhất `PAY` + 8 ký tự Base32, khóa chống trùng lặp tất định UUIDv5), nộp bằng chứng thanh toán 2 pha lưu trữ Cloudinary và chụp nhanh thông tin ngân hàng chủ nợ, xác nhận thủ công (chuyển nợ sang đã quyết toán) hoặc từ chối (kèm lý do bắt buộc, hoàn trả nợ về chờ thanh toán), nhắc nợ thủ công (tối đa 3 lần, giãn cách $\ge 24$ giờ), và tác vụ quét tự động hàng giờ (`settlement_scan`) để nhắc nợ tồn đọng sau 72 giờ và cảnh báo bằng chứng chờ duyệt sau 48 giờ.
- **Thông báo:** Thông báo trong ứng dụng nguyên tử với giao dịch và tác vụ River `send_notification`, thông báo đẩy Firebase Cloud Messaging (FCM) định tuyến đến phiên hoạt động mới nhất của người dùng, trung tâm thông báo với đánh dấu đã đọc lạc quan, và bộ phân giải điều hướng deep linking theo loại thông báo.
- **Quản trị Hệ thống & Cổng Web Admin:** Cổng Web Admin tĩnh được nhúng trực tiếp (`//go:embed` tại `/admin-portal/`), danh bạ tài khoản người dùng hiển thị số tài khoản ngân hàng che mặt nạ, chuyển đổi trạng thái tài khoản (`active`, `suspended`, `locked`) kèm thu hồi phiên tức thì và thông báo SSE, cảnh báo công nợ tài chính, bảng điều khiển giám sát chỉ số hệ thống thời gian thực, và các probe kiểm tra sức khỏe (`/health`, `/health/live`, `/health/ready`).
- **Hạ tầng Thời gian thực (Realtime):** Luồng SSE đơn cho mỗi phiên (`GET /api/v1/users/me/events`), bộ lắng nghe PostgreSQL dùng chung duy nhất cho các kênh `bill_events`, `group_events`, `user_events`, giải quyết xung đột thay thế kết nối dựa trên thứ tự commit PostgreSQL, sự kiện vô hiệu hóa dung lượng nhẹ với cơ chế debounce 250ms, vá danh sách tại chỗ (in-place list patching), và tự động kết nối lại kèm tái đồng bộ hoàn toàn khi nhận sự kiện `ready` hoặc phát lại delta qua `/sync`.

**Phân kỳ Phát hành (Phạm vi V1 vs V2):**
- **V1 (Phạm vi Production Hiện tại):** Trưởng nhóm trực tiếp chốt hóa đơn (chốt đơn lẻ và chốt hàng loạt bất đồng bộ kèm khóa nộp hóa đơn); tự động tạo công nợ; điều phối thanh toán qua VietQR động và chủ nợ xác nhận thủ công.
- **V2 (Mục tiêu Kế hoạch - Spec 0007):** Quy trình Đồng thuận Hóa đơn Con nợ (Debtor Bill Consent), trong đó các hóa đơn đang được kiểm tra sẽ tạo yêu cầu phê duyệt bất biến cho từng con nợ và yêu cầu con nợ chấp thuận rõ ràng trước khi Trưởng nhóm có thể chốt hóa đơn.

**Ngoài Phạm vi Triển khai:**
- **Lưu giữ Tiền & Chuyển tiền Tự động:** Nắm giữ tiền gửi của người dùng, quản lý số dư ví trả trước hoặc đóng vai trò cổng thanh toán trung gian theo Nghị định 52/2024/NĐ-CP.
- **Tự động Đối soát Trực tiếp với Core Banking:** Tích hợp API sản xuất trực tiếp với hệ thống core banking hoặc webhook của ngân hàng để tự động gạch nợ mà không cần chủ nợ xác nhận.
- **Công cụ Tài chính Phức tạp:** Bù trừ công nợ đa phương giữa các thành viên (ví dụ: A nợ B, B nợ C $\implies$ A nợ C), chấm điểm tín dụng, cho vay tiêu dùng (Mua trước trả sau - BNPL), giao dịch tiền mã hóa, hoặc xử lý khiếu nại tra soát tự động.

---

## **2. Tổng quan Sản phẩm** {#2-tổng-quan-sản-phẩm}

**PaySplit** là hệ thống chia tiền hóa đơn thông minh được thiết kế tối ưu cho nền tảng di động và ví điện tử. Hệ thống kết hợp trích xuất hóa đơn bằng Vision LLM, phân bổ món theo tỷ trọng, tính toán số học số nguyên chính xác, tạo VietQR động và xác thực thủ công bởi chủ nợ thành một quy trình làm việc thống nhất, minh bạch.

![][image4]

###### **Hình 3. Sơ đồ Bối cảnh Hệ thống** {#hình-3-sơ-đồ-bối-cảnh-hệ-thống}

---

## **3. Yêu cầu Người dùng** {#3-yêu-cầu-người-dùng}

### **3.1 Các Tác nhân (Actors)** {#31-các-tác-nhân-actors}

| STT | Tác nhân | Mô tả vai trò |
| :-: | :--- | :--- |
| 1 | **Khách vãng lai (Guest)** _(Con người)_ | Người dùng chưa xác thực thực hiện đăng ký tài khoản, xác thực email qua mã OTP 6 chữ số, hoặc yêu cầu đặt lại mật khẩu. |
| 2 | **Người dùng Đã xác thực (Authenticated User)** _(Con người chính)_ | Người dùng đã đăng nhập có phiên hoạt động hợp lệ, quản lý cài đặt cá nhân, tài khoản ngân hàng và các nhóm tham gia. |
| 3 | **Trưởng nhóm (Captain)** _(Con người chính)_ | Quản trị viên của một nhóm cụ thể, chịu trách nhiệm mời/xóa thành viên, chuyển giao vai trò trưởng nhóm, khóa/mở khóa nộp hóa đơn, chốt hóa đơn đơn lẻ/hàng loạt, và hủy hóa đơn. |
| 4 | **Chủ nợ (Creditor)** _(Con người chính)_ | Thành viên nhóm đã trả tiền trước cho một hóa đơn chung. Tải ảnh hóa đơn lên, xem xét bản nháp OCR, phân bổ món, nhận thanh toán qua VietQR, và xác nhận hoặc từ chối bằng chứng thanh toán. |
| 5 | **Người trả tiền / Con nợ (Payer / Debtor)** _(Con người chính)_ | Thành viên có nghĩa vụ hoàn trả cho Chủ nợ phần tiền của mình từ hóa đơn đã chốt. Quét mã VietQR, hoàn tất chuyển khoản qua app ngân hàng bên ngoài, và nộp bằng chứng thanh toán. |
| 6 | **Quản trị viên (Admin)** _(Quản trị hệ thống)_ | Người vận hành nền tảng nội bộ quản lý trạng thái tài khoản người dùng (hoạt động, tạm khóa, khóa vĩnh viễn), xem xét nhật ký kiểm toán, và giám sát sức khỏe hệ thống, độ sâu hàng đợi cùng các chỉ số qua Cổng Web Admin. |
| 7 | **Nhà cung cấp OCR (OCR Provider)** _(Hệ thống bên ngoài)_ | Dịch vụ Vision LLM (LlamaExtract / Gemini Flash) tiếp nhận ảnh hóa đơn và trích xuất dữ liệu có cấu trúc gồm tên quán, danh sách món, phụ phí, thuế và tổng tiền. |
| 8 | **Dịch vụ Thông báo Đẩy (FCM)** _(Hệ thống bên ngoài)_ | Nền tảng Firebase Cloud Messaging chuyển phát thông báo đẩy chạy nền tới thiết bị của người dùng. |
| 9 | **Dịch vụ Lưu trữ Đám mây (Cloudinary)** _(Hệ thống bên ngoài)_ | Dịch vụ lưu trữ đối tượng chứa ảnh hóa đơn, ảnh chụp màn hình bằng chứng chuyển khoản và ảnh đại diện người dùng. |
| 10 | **Danh bạ VietQR / NAPAS** _(Hệ thống bên ngoài)_ | Đặc tả định dạng QR liên ngân hàng và danh bạ ngân hàng dùng để kiểm tra tính hợp lệ của mã ngân hàng và tạo payload thanh toán VietQR chuẩn. |

##### **Bảng 2. Tất cả Tác nhân trong Hệ thống** {#bảng-2-tất-cả-tác-nhân-trong-hệ-thống}

---

### **3.2 Use Cases** {#32-use-cases}

#### **_3.2.1 Sơ đồ Use Case_** {#321-sơ-đồ-use-case}

## **![][image5]**

###### **Hình 4. Sơ đồ Use Case PaySplit** {#hình-4-sơ-đồ-use-case-paysplit}

![][image6]

###### **Hình 5. Sơ đồ Use Case Xác thực PaySplit** {#hình-5-sơ-đồ-use-case-xác-thực-paysplit}

#### **_3.2.2 Mô tả Use Case_** {#322-mô-tả-use-case}

| Mã | Tên Use Case | Tác nhân | Mô tả Use Case |
| :-: | :--- | :--- | :--- |
| 01 | Đăng nhập (Sign In) | Khách vãng lai | Xác thực bằng email/mật khẩu; thực thi chính sách một phiên hoạt động duy nhất bằng cách thu hồi phiên cũ (`replaced_by_sign_in`); áp dụng bảo vệ chống brute-force (5 lần sai/15p $\implies$ khóa 15p); trả về JWT (15p) và Refresh Token (7 ngày). |
| 02 | Đăng ký (Sign Up) | Khách vãng lai | Đăng ký tài khoản mới với số điện thoại Việt Nam bắt buộc, email, tên hiển thị và mật khẩu; tạo người dùng ở trạng thái `pending_verification`; gửi mã OTP 6 chữ số băm SHA-256. |
| 03 | Xác thực Email (Verify Email) | Khách vãng lai | Nhập mã OTP 6 chữ số; xác thực với tối đa 5 lần thử sai (lần thứ 5 sai sẽ vô hiệu hóa token vĩnh viễn); kích hoạt trạng thái tài khoản thành `active`; yêu cầu đăng nhập thủ công sau khi xác thực. |
| 04 | Gửi lại OTP Xác thực | Khách vãng lai | Yêu cầu mã OTP xác thực mới; giới hạn tần suất theo email và IP; luôn trả về HTTP 202 để chống dò quét tài khoản. |
| 05 | Làm mới Token (Xoay vòng Phiên) | Người dùng Đã xác thực | Xoay vòng cặp access/refresh token; xác thực refresh token dùng một lần; phát hiện hành vi tái sử dụng token đã dùng và thu hồi phiên ngay lập tức (`SESSION_REVOKED`). |
| 06 | Quên & Đặt lại Mật khẩu | Khách vãng lai | Yêu cầu đặt lại mật khẩu qua email OTP (luôn trả về HTTP 202); xác thực OTP và thiết lập mật khẩu mới; thu hồi **toàn bộ** các phiên đang hoạt động (`password_reset`). |
| 07 | Đăng xuất (Sign Out) | Người dùng Đã xác thực | Chấm dứt phiên hiện tại bằng middleware `TokenAuth` (xác thực chữ ký JWT mà không yêu cầu phiên DB còn active); xóa FCM token khỏi phiên; giữ lại `device_id` cục bộ. |
| 08 | Đổi Mật khẩu | Người dùng Đã xác thực | Thay thế mật khẩu hiện tại sau khi kiểm tra mật khẩu cũ; cập nhật hash mật khẩu và thu hồi tất cả các phiên khác (`password_changed`) trong khi vẫn giữ phiên hiện tại hoạt động. |
| 09 | Cập nhật Hồ sơ & Ngân hàng | Người dùng Đã xác thực | Cập nhật tên hiển thị, số điện thoại, tài khoản ngân hàng mặc định (quy tắc đủ cả 3 trường hoặc để trống, kiểm tra danh bạ VietQR), và ảnh đại diện (chuyển đổi WebP qua Cloudinary kèm dọn dẹp đền bù tự động). |
| 10 | Tạo Nhóm Mới | Người dùng Đã xác thực | Tạo một nhóm chi tiêu (1–100 ký tự, tiền tệ VND); người tạo tự động trở thành Trưởng nhóm; khởi tạo sổ cái nhóm và nhật ký hoạt động. |
| 11 | Tạo Liên kết Mời vào Nhóm | Trưởng nhóm, Thành viên | Tạo mã mời Base62 8 ký tự phân biệt hoa thường (mặc định 24h, cấu hình 1–168h, tùy chọn giới hạn số lượt); Trưởng nhóm có quyền cấu hình hoặc tạo mới; thành viên thường tái sử dụng mã mời đang hoạt động. |
| 12 | Xem trước & Tham gia Nhóm | Người dùng Đã xác thực | Xem trước thông tin nhóm với giới hạn kép (30 req/phút); tham gia nhóm dưới khóa mức hàng (`LockActiveGroup`); thực thi giới hạn sức chứa 50 thành viên; kích hoạt lại thành viên đã rời nhóm để bảo toàn lịch sử sổ cái. |
| 13 | Xóa Thành viên / Rời Nhóm | Trưởng nhóm, Thành viên | Xóa thành viên hoặc tự rời nhóm; bị chặn nếu thành viên còn công nợ chưa quyết toán (`409 GROUP_MEMBER_HAS_OPEN_DEBTS`); Trưởng nhóm không thể rời nhóm nếu chưa chuyển giao vai trò; chuyển trạng thái thành viên sang `inactive`. |
| 14 | Chuyển giao Vai trò Trưởng nhóm | Trưởng nhóm | Chuyển quyền trưởng nhóm cho một thành viên đang hoạt động bằng khóa hàng `NOWAIT` và khóa theo thứ tự UUID tăng dần để chống deadlock. |
| 15 | Giải tán (Lưu trữ) Nhóm | Trưởng nhóm | Lưu trữ nhóm; bị chặn nếu đang có tiến trình chốt hàng loạt hoặc còn hóa đơn/công nợ chưa quyết toán; hủy kích hoạt tất cả thành viên và thu hồi các mã mời. |
| 16 | Khóa / Mở khóa Nộp Hóa đơn | Trưởng nhóm | Khóa tiếp nhận hóa đơn mới của nhóm (`bill_submission_locked_at`) ngăn tạo hóa đơn mới; Trưởng nhóm có thể mở khóa bất kỳ lúc nào. |
| 17 | Tải ảnh Hóa đơn (OCR) | Chủ nợ, Thành viên | Tải lên 1–5 ảnh hóa đơn ($\le 10\text{MB}$); tạo hóa đơn nháp (phiên bản 1) và đẩy job River `bill_ocr` vào hàng đợi trong cùng một transaction; trả về HTTP 202 Accepted. |
| 18 | Trích xuất Dữ liệu qua Worker | Nhà cung cấp OCR | Worker River ghép nối các ảnh theo chiều dọc, gọi LlamaExtract, thử lại nếu gặp lỗi mạng tạm thời (tối đa 3 lần), lưu kết quả JSONB ứng viên và phát sự kiện `ocr.updated`. |
| 19 | Cập nhật Hóa đơn Nháp & Gán món | Chủ nợ, Trưởng nhóm | Chỉnh sửa tên quán, danh sách món, thuế, giảm giá và phân bổ trọng số (`big.Rat`); suy ra tổng tiền thực tế; thực thi khóa lạc quan qua `version` CAS; tự động chuyển hóa đơn đã kiểm tra về trạng thái nháp khi sửa. |
| 20 | Kiểm tra Hóa đơn (Review) | Chủ nợ, Trưởng nhóm | Kiểm tra toàn bộ các quy tắc phân bổ và điều kiện chặn (`ITEM_UNASSIGNED`, `INACTIVE_MEMBER_ASSIGNED`, `DISCOUNT_EXCEEDS_BILL`, `SUBTOTAL_MISMATCH`, `TOTAL_MISMATCH`); chuyển hóa đơn hợp lệ sang trạng thái `reviewed`. |
| 21 | Chốt Hóa đơn (Finalize) | Trưởng nhóm | Khóa bất biến hóa đơn; tính toán phần tiền chính xác qua thuật toán làm tròn số dư lớn nhất Hamilton không thiên vị chủ nợ (phá vỡ hòa 1 VNĐ theo thứ tự UUID); tạo các bản ghi công nợ `awaiting`, ghi nhật ký và đẩy thông báo FCM. |
| 22 | Chốt Hàng loạt Tất cả Hóa đơn | Trưởng nhóm | Tự động khóa nộp hóa đơn; chụp nhanh toàn bộ hóa đơn nháp/đã kiểm tra thành các mục trong đợt; xử lý từng hóa đơn trong một transaction job River độc lập; thông báo cho Trưởng nhóm khi hoàn tất. |
| 23 | Hủy bỏ Hóa đơn Đã chốt | Trưởng nhóm | Hủy hóa đơn đã chốt kèm lý do bắt buộc (1–500 ký tự); bị chặn nếu đã nộp bằng chứng thanh toán hoặc đang xử lý; tự động hủy các intent mã QR đang chờ; hủy tất cả các khoản nợ liên quan. |
| 24 | Xem Phân bổ Chi phí Chi tiết | Con nợ, Thành viên | Hiển thị chi tiết từng món được phân bổ, phụ phí/giảm giá theo tỷ lệ, điều chỉnh làm tròn chính xác (+1/0 VNĐ), số dư nhóm ròng và thông tin thanh toán. |
| 25 | Tạo VietQR Thanh toán | Con nợ | Tạo mã VietQR cho 1–100 khoản nợ `awaiting` đối với một chủ nợ; sử dụng khóa chống trùng lặp tất định UUIDv5; tạo intent thanh toán không khóa nợ `pending_proof` kèm mã tham chiếu duy nhất (`PAY` + 8 ký tự Base32). |
| 26 | Nộp Bằng chứng Thanh toán | Con nợ | Quy trình 2 pha: giữ trước khóa chống trùng lặp ở trạng thái `in_progress`, tải ảnh chụp màn hình lên Cloudinary, chụp nhanh thông tin ngân hàng chủ nợ, chuyển trạng thái thanh toán và công nợ sang `pending_confirmation`, và thông báo cho Chủ nợ. |
| 27 | Xác nhận / Từ chối Thanh toán | Chủ nợ | Chủ nợ xác nhận thủ công (chuyển thanh toán sang `confirmed` và công nợ sang `settled`) hoặc từ chối (kèm lý do bắt buộc, hoàn trả công nợ về `awaiting` và xóa `payment_id`). |
| 28 | Nhắc nợ & Quét Tự động | Chủ nợ, Trưởng nhóm, Hệ thống | Nhắc nợ thủ công (tối đa 3 lần, giãn cách $\ge 24$h, khóa UUIDv4); job River hàng giờ (`settlement_scan`) gửi nhắc nhở tự động cho các khoản nợ tồn đọng 72h (dùng chung hạn mức 3 lần) và cảnh báo chủ nợ về bằng chứng chờ duyệt sau 48h. |
| 29 | Thông báo Trong app & Đẩy | Người dùng Đã xác thực | Liệt kê danh sách thông báo phân trang kèm đánh dấu đã đọc lạc quan; gửi thông báo đẩy FCM bất đồng bộ; điều hướng chính xác khi nhấn vào thông báo dựa trên deep linking. |
| 30 | Cổng Web Admin & Giám sát | Quản trị viên | Cổng Web Admin tĩnh nhúng trực tiếp (`/admin-portal/`); quản lý tài khoản; cập nhật trạng thái (active, suspended, locked) kèm thu hồi phiên tức thì và thông báo SSE; xem chỉ số tổng quan hệ thống và kiểm tra probe sức khỏe. |
| 31 | Đồng bộ Bắt kịp Delta Nhóm | Người dùng Đã xác thực | Lấy các delta sự kiện nhóm bị bỏ lỡ (`GET /api/v1/groups/{id}/sync?since=N`) khi client kết nối lại hoặc phát hiện chênh lệch phiên bản; khôi phục trạng thái mà không cần tải lại toàn bộ trang. |

##### **Bảng 3. Bảng Mô tả Use Case** {#bảng-3-bảng-mô-tả-use-case}

---

## **4. Yêu cầu Chức năng** {#4-yêu-cầu-chức-năng}

### **4.1 Các Tính năng Cốt lõi của Hệ thống** {#41-các-tính-năng-cốt-lõi-của-hệ-thống}

#### **_4.1.1 - Đăng nhập (Sign In)_** {#411---đăng-nhập-sign-in}

- **_Kích hoạt Chức năng:_** Khách vãng lai gửi thông tin đăng nhập trên màn hình đăng nhập (`POST /api/v1/auth/sign-in`).
- **_Mô tả Chức năng:_** Xác thực người dùng, thực thi chính sách một phiên hoạt động duy nhất, áp dụng giới hạn tần suất chống brute-force, và cấp phát cặp JWT Access Token cùng Refresh Token.
- **_Chi tiết Chức năng:_**
  - **Kiểm tra Dữ liệu:** Email phải đúng định dạng email hợp lệ; mật khẩu không được để trống; tùy chọn gửi `device_id` cố định (UUID) và `fcm_token`.
  - **Quy tắc Một Phiên Hoạt động Duy nhất:** Mỗi người dùng chỉ có tối đa một phiên hoạt động (`revoked_at IS NULL`, được bảo đảm bằng unique index `uq_sessions_one_active_per_user`). Khi đăng nhập trên thiết bị mới, hệ thống ngay lập tức thu hồi mọi phiên đang hoạt động trước đó với lý do `replaced_by_sign_in`.
  - **Thứ tự Thực thi (Cổng Bảo mật):**
    1. Hệ thống kiểm tra `login_blocked_until` **trước khi** kiểm tra mật khẩu. Nếu đang bị khóa, trả về HTTP `429 RATE_LIMITED` kèm header `Retry-After`.
    2. Hệ thống truy vấn người dùng theo email. Nếu không tìm thấy, thực hiện ghi nhận thất bại giả lập và trả về HTTP `401 INVALID_CREDENTIALS` (chống dò quét tài khoản).
    3. Hệ thống xác thực hash mật khẩu bằng bcrypt (`DefaultCost = 10`). Nếu thất bại, tăng bộ đếm số lần sai (5 lần sai trong 15 phút sẽ khóa tài khoản 15 phút) và trả về HTTP 401 (hoặc 429 nếu vừa bị khóa).
    4. Mật khẩu chính xác: Hệ thống kiểm tra `status` tài khoản. Nếu `pending_verification`, trả về HTTP `403 EMAIL_NOT_VERIFIED`. Nếu `suspended` hoặc `locked`, trả về HTTP `403 ACCOUNT_UNAVAILABLE`.
    5. Tài khoản hợp lệ: Hệ thống thu hồi phiên cũ, tạo bản ghi phiên mới lưu hash SHA-256 của refresh token, liên kết `device_id`, cấp Access Token 15 phút (JWT chứa `sub`, `role`, `sid`) và Refresh Token 7 ngày, gắn FCM token tùy chọn, và trả về HTTP 200.
  - **Xử lý phía Client:** Ứng dụng Flutter lưu `access_token`, `refresh_token`, và `device_id` vào `flutter_secure_storage`, khởi tạo trình quản lý FCM, và điều hướng tới `/home`.

---

#### **_4.1.2 - Đăng ký (Sign Up)_** {#412---đăng-ký-sign-up}

- **_Kích hoạt Chức năng:_** Khách vãng lai gửi biểu mẫu đăng ký (`POST /api/v1/auth/sign-up`).
- **_Mô tả Chức năng:_** Đăng ký tài khoản người dùng mới ở trạng thái `pending_verification` và gửi mã OTP xác thực 6 chữ số.
- **_Chi tiết Chức năng:_**
  - **Kiểm tra Dữ liệu:** Tên hiển thị (1–100 runes; FE tối thiểu 2 ký tự); email (chữ thường, cú pháp hợp lệ); mật khẩu (8–72 bytes, bắt buộc có chữ hoa, chữ thường và chữ số); số điện thoại (**bắt buộc**, duy nhất, khớp định dạng E.164 Việt Nam `0[35789]...`).
  - **Giới hạn Tần suất:** Tối đa 10 yêu cầu đăng ký mỗi giờ trên mỗi địa chỉ IP, thực thi qua khóa tư vấn cơ sở dữ liệu (`pg_advisory_xact_lock`).
  - **Thực thi:**
    - Tạo khóa chính UUID v7 và chỉ lưu hash mật khẩu bcrypt.
    - Tạo mã OTP số gồm 6 chữ số (hạn dùng 10 phút) và lưu hash SHA-256 vào bảng `user_tokens`.
    - Gửi email xác thực qua Gmail SMTP. Nếu gửi SMTP thất bại, lỗi được ghi log, trường `verification_email_sent` được đặt thành `false`, và quá trình đăng ký vẫn thành công với HTTP `201 Created` (đảm bảo sự cố SMTP không làm gián đoạn việc đăng ký của người dùng).
    - Client chuyển hướng sang màn hình `/verify-otp` truyền theo email đã đăng ký.

---

#### **_4.1.3 - Xác thực Email (Verify Email)_** {#413---xác-thực-email-verify-email}

- **_Kích hoạt Chức năng:_** Khách vãng lai nhập mã OTP 6 chữ số trên màn hình xác thực (`POST /api/v1/auth/verify-email`).
- **_Mô tả Chức năng:_** Xác thực OTP email, kích hoạt tài khoản và đánh dấu token đã được sử dụng.
- **_Chi tiết Chức năng:_**
  - **Kiểm tra Dữ liệu:** Email và mã OTP 6 chữ số.
  - **Thực thi:**
    - Truy vấn người dùng và token với khóa `FOR UPDATE`.
    - Nếu người dùng đã `active` (ví dụ: vô tình bấm gửi 2 lần), thực hiện so sánh hằng số thời gian với token đã dùng trước đó và trả về HTTP 200 (phát lại idempotent).
    - Nếu OTP không chính xác, tăng `attempt_count`. Nếu `attempt_count >= 5`, token bị **vô hiệu hóa vĩnh viễn** (không thể dùng lại, bắt buộc phải gửi lại OTP mới). Trả về HTTP `400 INVALID_OR_EXPIRED_TOKEN`.
    - Nếu OTP hợp lệ và chưa hết hạn, cập nhật `users.status = 'active'`, đánh dấu token `used_at = now()`, và trả về HTTP 200.
    - **Không cấp JWT tại bước này.** Client điều hướng người dùng về màn hình `/login` để nhập mật khẩu đăng nhập.

---

#### **_4.1.4 - Gửi lại OTP Xác thực (Resend Verification OTP)_** {#414---gửi-lại-otp-xác-thực-resend-verification-otp}

- **_Kích hoạt Chức năng:_** Khách vãng lai yêu cầu OTP mới trên màn hình xác thực (`POST /api/v1/auth/resend-verification`).
- **_Mô tả Chức năng:_** Cấp và gửi email mã OTP xác thực 6 chữ số mới.
- **_Chi tiết Chức năng:_**
  - **Giới hạn Tần suất:** Thực thi độc lập theo hash(email) và hash(IP) với giãn cách tối thiểu 1 yêu cầu mỗi phút và tối đa 10 yêu cầu mỗi giờ.
  - **Chống Dò quét:** Nếu email không tồn tại hoặc tài khoản đã kích hoạt, hệ thống vẫn trả về HTTP `202 Accepted` mà không gửi email.
  - **Thực thi:** Tạo mã OTP 6 chữ số mới, vô hiệu hóa token cũ đang hoạt động (`uq_user_tokens_one_active_per_type`), và gửi email.

---

#### **_4.1.5 - Làm mới Token & Xoay vòng Phiên (Token Refresh & Session Rotation)_** {#415---làm-mới-token--xoay-vòng-phiên-token-refresh--session-rotation}

- **_Kích hoạt Chức năng:_** Client Flutter nhận mã lỗi HTTP 401 khi gọi REST hoặc luồng SSE đã xác thực (`POST /api/v1/auth/refresh`).
- **_Mô tả Chức năng:_** Xoay vòng refresh token, xác thực liên kết thiết bị, phát hiện hành vi tấn công tái sử dụng token, và cấp cặp access/refresh token mới.
- **_Chi tiết Chức năng:_**
  - **Điều phối Đơn chuyến (Single-Flight):** Client Flutter thực thi làm mới token thông qua trình quản lý single-flight (`SessionRefresher`), đảm bảo các yêu cầu REST và SSE cùng bị 401 sẽ cùng chờ một thao tác refresh duy nhất thay vì kích hoạt xoay vòng token đồng thời.
  - **Thực thi:**
    - Kiểm tra `refresh_token` và `device_id`.
    - Băm token bằng SHA-256 và truy vấn bảng `refresh_tokens`.
    - **Phát hiện Tái sử dụng:** Nếu token đã từng được sử dụng (`used_at IS NOT NULL`), hệ thống coi đây là hành vi đánh cắp token: ngay lập tức thu hồi toàn bộ phiên và tất cả refresh token liên quan với lý do `refresh_reuse`, phát sự kiện SSE `session.ended`, và trả về HTTP `401 SESSION_REVOKED`. Client xóa sạch bộ nhớ cục bộ và chuyển hướng về `/welcome`.
    - Nếu token không hợp lệ, hết hạn hoặc gắn với `device_id` khác, trả về HTTP `400 INVALID_OR_EXPIRED_TOKEN`.
    - Nếu hợp lệ, đánh dấu token cũ `used_at = now()`, chèn refresh token mới 7 ngày (bị giới hạn bởi `session.expires_at`), ký access token mới 15 phút, và trả về HTTP 200.

---

#### **_4.1.6 - Quên mật khẩu & Đặt lại mật khẩu (Forgot & Reset Password)_** {#416---quên-mật-khẩu--đặt-lại-mật-khẩu-forgot--reset-password}

- **_Kích hoạt Chức năng:_** Người dùng yêu cầu khôi phục mật khẩu từ màn hình đăng nhập.
- **_Mô tả Chức năng:_** Cấp OTP khôi phục 6 chữ số có giới hạn thời gian và đặt lại mật khẩu tài khoản.
- **_Chi tiết Chức năng:_**
  - **Quên Mật khẩu (`POST /api/v1/auth/forgot-password`):** Gửi email; giới hạn tần suất theo email và IP; luôn trả về HTTP `202 Accepted` để chống dò quét tài khoản. Tạo mã OTP 6 chữ số băm SHA-256 (hạn 10 phút) và gửi email đặt lại mật khẩu.
  - **Đặt lại Mật khẩu (`POST /api/v1/auth/reset-password`):** Gửi email, OTP 6 chữ số và mật khẩu mới. Kiểm tra chính sách mật khẩu; cập nhật hash bcrypt; đánh dấu OTP đã sử dụng; **thu hồi TOÀN BỘ phiên đang hoạt động** (`password_reset`) của người dùng đó. Trả về HTTP 204. Client chuyển về màn hình `/login` kèm thông báo thành công màu xanh lá.

---

#### **_4.1.7 - Đăng xuất (Sign Out)_** {#417---đăng-xuất-sign-out}

- **_Kích hoạt Chức năng:_** Người dùng đã xác thực chọn Đăng xuất từ cài đặt hồ sơ (`POST /api/v1/auth/sign-out`).
- **_Mô tả Chức năng:_** Chấm dứt phiên thiết bị hiện tại và xóa token cục bộ.
- **_Chi tiết Chức năng:_**
  - Sử dụng middleware `TokenAuth` (xác thực chữ ký JWT mà không yêu cầu phiên database phải còn active), cho phép người dùng đăng xuất sạch sẽ ngay cả khi phiên của họ đã bị thu hồi trước đó.
  - Thu hồi phiên `sid` trong PostgreSQL với lý do `sign_out`.
  - Client hủy đăng ký FCM token khỏi thiết bị (`onLogout`), xóa `access_token` và `refresh_token` khỏi secure storage nhưng **giữ lại `device_id`**, và điều hướng về `/welcome`. Các lỗi mạng xảy ra trong quá trình đăng xuất được bỏ qua an toàn để đảm bảo đăng xuất cục bộ luôn thành công.

---

#### **_4.1.8 - Đổi mật khẩu (Change Password)_** {#418---đổi-mật-khẩu-change-password}

- **_Kích hoạt Chức năng:_** Người dùng đã xác thực gửi biểu mẫu đổi mật khẩu (`PUT /api/v1/users/me/password`).
- **_Mô tả Chức năng:_** Cập nhật mật khẩu tài khoản trong khi vẫn duy trì phiên hoạt động hiện tại.
- **_Chi tiết Chức năng:_**
  - Xác thực hash bcrypt của mật khẩu hiện tại. Nếu sai, trả về HTTP `400 INVALID_CURRENT_PASSWORD`.
  - Kiểm tra độ mạnh mật khẩu mới (8–72 bytes, chữ hoa, chữ thường, số) và đảm bảo mật khẩu mới khác mật khẩu cũ (`400 VALIDATION_FAILED`).
  - Cập nhật hash bcrypt mật khẩu mới và **thu hồi tất cả các phiên hoạt động KHÁC** (`password_changed`) trong khi **vẫn giữ phiên thiết bị hiện tại (`sid`) hoạt động**. Trả về HTTP 204. Người dùng tiếp tục sử dụng ứng dụng bình thường.

---

#### **_4.1.9 - Cập nhật Hồ sơ & Thông tin Ngân hàng (Update Profile & Bank Info)_** {#419---cập-nhật-hồ-sơ--thông-tin-ngân-hàng-update-profile--bank-info}

- **_Kích hoạt Chức năng:_** Người dùng đã xác thực cập nhật thông tin cá nhân, tài khoản ngân hàng hoặc tải ảnh đại diện (`PATCH /api/v1/users/me`, `PUT /api/v1/users/me/avatar`).
- **_Mô tả Chức năng:_** Quản lý thông tin cá nhân, tài khoản ngân hàng nhận tiền để tạo VietQR, và ảnh đại diện.
- **_Chi tiết Chức năng:_**
  - **Hồ sơ & Tài khoản Ngân hàng (`PATCH /api/v1/users/me`):**
    - Thông tin ngân hàng thực thi quy tắc **đủ-cả-ba-hoặc-để-trống**: `bank_code`, `bank_account_number`, và `bank_account_holder` phải cùng có mặt hoặc cùng nhận giá trị null.
    - `bank_code` được kiểm tra tính hợp lệ dựa trên danh bạ VietQR NAPAS (`400 UNSUPPORTED_BANK`).
    - `bank_account_number` phải gồm 6–19 chữ số.
    - `bank_account_holder` được tự động chuẩn hóa sang định dạng chữ in hoa không dấu (ví dụ: `NGUYEN VAN A`).
  - **Tải ảnh Đại diện (`PUT /api/v1/users/me/avatar`):**
    - Tiếp nhận ảnh multipart dung lượng tối đa 10MB (JPEG, PNG, GIF, WebP).
    - Máy chủ chuyển đổi ảnh sang WebP (chất lượng 82, kích thước tối đa 1024px, loại bỏ EXIF) dưới giới hạn semaphore đồng thời (`AVATAR_MAX_CONCURRENT_CONVERSIONS = 2`). Định dạng HEIC/không hỗ trợ được tải lên nguyên bản.
    - Tải ảnh lên Cloudinary (`paysplit/avatars/{uid}/{uuidv7}`) **trước khi** cập nhật cơ sở dữ liệu.
    - Nếu cập nhật DB thất bại, máy chủ thực hiện xóa đền bù ảnh mới trên Cloudinary (không đẩy job vào hàng đợi).
    - Nếu xóa ảnh đại diện cũ trên Cloudinary thất bại, máy chủ đẩy job nền vào bảng `media_cleanup_jobs` (thử lại tối đa 10 lần qua worker ticker nội bộ). Trả về HTTP 200 kèm `avatar_url`.

---

#### **_4.1.10 - Tạo Nhóm Mới (Create New Group)_** {#4110---tạo-nhóm-mới-create-new-group}

- **_Kích hoạt Chức năng:_** Người dùng đã xác thực tạo một nhóm chi tiêu (`POST /api/v1/groups`).
- **_Mô tả Chức năng:_** Tạo một nhóm chi tiêu, gán người tạo làm Trưởng nhóm và khởi tạo theo dõi sổ cái.
- **_Chi tiết Chức năng:_**
  - **Kiểm tra Dữ liệu:** Tên nhóm (1–100 runes; FE yêu cầu 3–50 ký tự); tiền tệ mặc định là `VND`.
  - **Thực thi:** Trong một transaction duy nhất: chèn bản ghi nhóm (`status = 'active'`), chèn người tạo làm thành viên hoạt động đầu tiên với vai trò `captain`, ghi nhật ký hoạt động `group_created`, và trả về HTTP `201 Created` kèm chi tiết nhóm và tư cách thành viên. Client chuyển hướng sang màn hình Thêm Thành viên.

---

#### **_4.1.11 - Tạo Liên kết Mời vào Nhóm (Generate Group Invite)_** {#4111---tạo-liên-kết-mời-vào-nhóm-generate-group-invite}

- **_Kích hoạt Chức năng:_** Người dùng yêu cầu liên kết mời nhóm để chia sẻ (`POST /api/v1/groups/{id}/invites`, `GET /api/v1/groups/{id}/invites`).
- **_Mô tả Chức năng:_** Tạo mới hoặc tái sử dụng liên kết mời Base62 gồm 8 ký tự.
- **_Chi tiết Chức năng:_**
  - **Định dạng Mã Mời:** Chuỗi ngẫu nhiên 8 ký tự Base62 (phân biệt hoa thường). Hạn sử dụng mặc định là 24 giờ (có thể cấu hình 1–168 giờ). Tùy chọn `max_uses` (1–50; NULL nghĩa là không giới hạn).
  - **Quy tắc Quyền hạn:**
    - Nếu yêu cầu chỉ định `expires_in_hours`, `max_uses`, hoặc `regenerate = true`, người gọi **bắt buộc phải là Trưởng nhóm** (`403 FORBIDDEN` nếu không phải).
    - Nếu thành viên thường gọi với body trống, hệ thống trả về mã mời đang hoạt động sẵn có mà không thay đổi tham số.
  - **Xử lý Trùng lặp:** Xung đột trùng mã được thử lại tối đa 5 lần; trả về HTTP 200 kèm `invite_url` (được tạo từ `APP_INVITE_BASE_URL + code`, ví dụ: `https://paysplit.app/join/{code}`).

---

#### **_4.1.12 - Xem trước & Tham gia Nhóm (Preview & Join Group)_** {#4112---xem-trước--tham-gia-nhóm-preview--join-group}

- **_Kích hoạt Chức năng:_** Người dùng đã xác thực dán liên kết mời hoặc quét mã QR nhóm (`GET /api/v1/groups/invites/{code}`, `POST /api/v1/groups/join`).
- **_Mô tả Chức năng:_** Xem trước thông tin nhóm và gia nhập nhóm dưới cơ chế kiểm soát đồng thời.
- **_Chi tiết Chức năng:_**
  - **Giới hạn Tần suất:** Cả hai endpoint áp dụng giới hạn kép `RateLimitByAccountAndIP` (mặc định 30 yêu cầu/phút tính chung theo tài khoản và IP).
  - **Xem trước (`GET /groups/invites/{code}`):** Nếu mã mời hết hạn, bị thu hồi, hết lượt dùng hoặc nhóm đã bị lưu trữ, trả về lỗi chuẩn HTTP `404 INVITE_NOT_FOUND` (chống dò quét). Ngược lại trả về tên nhóm, số thành viên hoạt động và tên hiển thị của Trưởng nhóm.
  - **Tham gia (`POST /groups/join`):**
    - Lấy khóa mức hàng của nhóm (`LockActiveGroup` `FOR UPDATE`).
    - Nếu người dùng đã là thành viên đang hoạt động, trả về HTTP 200 `already_active` mà không trừ số lượt dùng của mã mời.
    - Nếu nhóm đã đạt sức chứa tối đa (50 thành viên hoạt động), trả về HTTP `409 GROUP_MEMBER_LIMIT_REACHED`.
    - Nếu người dùng từng là thành viên trước đây (`status = 'inactive'`), kích hoạt lại bản ghi thành viên cũ, đặt `role = 'member'`, `joined_at = now()`, giữ nguyên `member_id` lịch sử, và ghi nhật ký hoạt động `member_reactivated`.
    - Nếu là người dùng mới, chèn bản ghi vào `group_members`, tăng `use_count` của mã mời, ghi nhật ký hoạt động `member_joined`, và trả về HTTP 200 `joined`.

---

#### **_4.1.13 - Xóa Thành viên & Rời Nhóm (Remove Member & Leave Group)_** {#4113---xóa-thành-viên--rời-nhóm-remove-member--leave-group}

- **_Kích hoạt Chức năng:_** Trưởng nhóm xóa một thành viên hoặc thành viên tự rời nhóm (`DELETE /api/v1/groups/{id}/members/{memberId}`).
- **_Mô tả Chức năng:_** Hủy kích hoạt tư cách thành viên nhóm trong khi bảo toàn tính toàn vẹn của sổ cái tài chính.
- **_Chi tiết Chức năng:_**
  - **Cổng Bảo mật:** Người gọi phải là Trưởng nhóm hoặc chính thành viên đó (`403 FORBIDDEN` nếu không có quyền; thành viên không tồn tại trả về 403 để chống dò quét thành viên).
  - **Kiểm tra Vai trò Trưởng nhóm:** Trưởng nhóm không thể rời nhóm hoặc bị xóa nếu chưa chuyển giao vai trò Trưởng nhóm (`409 CAPTAIN_TRANSFER_REQUIRED`).
  - **Bất biến Công nợ Hai chiều:** Hệ thống kiểm tra tất cả các khoản nợ chưa quyết toán mà thành viên đó là con nợ (`payable`) hoặc chủ nợ (`receivable`). Nếu có bất kỳ khoản nợ nào ở trạng thái `awaiting` hoặc `pending_confirmation`, trả về HTTP `409 GROUP_MEMBER_HAS_OPEN_DEBTS` kèm số tiền phải trả và phải thu cụ thể.
  - **Thực thi:** Cập nhật `group_members.status = 'inactive'`, `left_at = now()`, ghi nhật ký hoạt động `member_left` hoặc `member_removed`, và trả về HTTP 204. Lịch sử phân bổ hóa đơn và các khoản nợ đã quyết toán vẫn được giữ nguyên vẹn.

---

#### **_4.1.14 - Chuyển giao Vai trò Trưởng nhóm (Transfer Captain Role)_** {#4114---chuyển-giao-vai-trò-trưởng-nhóm-transfer-captain-role}

- **_Kích hoạt Chức năng:_** Trưởng nhóm chuyển quyền quản lý nhóm cho một thành viên khác (`PUT /api/v1/groups/{id}/members/{memberId}/role`).
- **_Mô tả Chức năng:_** Chuyển giao quyền lãnh đạo nhóm một cách nguyên tử.
- **_Chi tiết Chức năng:_**
  - Body yêu cầu phải chỉ định `{ "role": "captain" }`. Người gọi phải là Trưởng nhóm hiện tại (`403 CAPTAIN_REQUIRED`). Mục tiêu không thể là chính mình (`400 VALIDATION_FAILED`).
  - Khóa nhóm với `FOR UPDATE NOWAIT`. Nếu có thao tác thay đổi khác đang giữ khóa, trả về ngay HTTP `409 CAPTAIN_TRANSFER_CONFLICT` thay vì bị nghẽn chờ.
  - Khóa cả hai bản ghi thành viên theo thứ tự UUID tăng dần để chống deadlock.
  - Giáng chức trưởng nhóm cũ xuống `member`, thăng chức thành viên mục tiêu lên `captain`, ghi nhật ký hoạt động `captain_transferred`, và trả về HTTP 200.

---

#### **_4.1.15 - Giải tán (Lưu trữ) Nhóm (Disband / Archive Group)_** {#4115---giải-tán-lưu-trữ-nhóm-disband--archive-group}

- **_Kích hoạt Chức năng:_** Trưởng nhóm giải tán nhóm (`DELETE /api/v1/groups/{id}`).
- **_Mô tả Chức năng:_** Lưu trữ nhóm sau khi xác nhận toàn bộ nghĩa vụ tài chính đã được thanh toán xong.
- **_Chi tiết Chức năng:_**
  - Người gọi phải là Trưởng nhóm. Hệ thống khóa nhóm.
  - Chặn nếu đang có đợt chốt hóa đơn hàng loạt đang chạy (`409 BULK_FINALIZE_IN_PROGRESS`).
  - Chặn nếu còn hóa đơn chưa chốt/chưa hủy hoặc còn khoản nợ chưa quyết toán (`409 GROUP_HAS_UNSETTLED_OBLIGATIONS`).
  - Cập nhật `groups.status = 'archived'`, hủy kích hoạt toàn bộ thành viên đang hoạt động, thu hồi tất cả mã mời, ghi nhật ký hoạt động `group_archived`, và trả về HTTP 204. Các yêu cầu xem chi tiết nhóm sau đó sẽ nhận HTTP `404 GROUP_NOT_FOUND`.

---

#### **_4.1.16 - Khóa và Mở khóa Nộp Hóa đơn (Lock / Unlock Bill Submissions)_** {#4116---khóa-và-mở-khóa-nộp-hóa-đơn-lock--unlock-bill-submissions}

- **_Kích hoạt Chức năng:_** Trưởng nhóm đóng hoặc mở lại việc nộp hóa đơn vào nhóm (`POST /api/v1/groups/{id}/bills/lock-submissions`, `POST /api/v1/groups/{id}/bills/unlock-submissions`).
- **_Mô tả Chức năng:_** Kiểm soát việc các thành viên có được phép gửi thêm hóa đơn mới vào nhóm hay không.
- **_Chi tiết Chức năng:_**
  - **Khóa Nộp Hóa đơn:** Đặt `groups.bill_submission_locked_at = now()`. Mọi nỗ lực tải lên hóa đơn sau đó sẽ bị từ chối với HTTP `409 BILL_SUBMISSION_LOCKED`. Phát sự kiện realtime `group.bill_submission_locked`.
  - **Mở khóa Nộp Hóa đơn:** Đặt `bill_submission_locked_at = NULL`. Khôi phục khả năng tải lên hóa đơn. Phát sự kiện `group.bill_submission_locked`.

---

#### **_4.1.17 - Tải ảnh Hóa đơn & Trích xuất OCR Bất đồng bộ (Upload Bill & Async OCR)_** {#4117---tải-ảnh-hóa-đơn--trích-xuất-ocr-bất-đồng-bộ-upload-bill--async-ocr}

- **_Kích hoạt Chức năng:_** Thành viên nhóm tải lên 1–5 ảnh chụp hóa đơn (`POST /api/v1/bills` multipart).
- **_Mô tả Chức năng:_** Lưu trữ ảnh hóa đơn, khởi tạo bản ghi hóa đơn nháp, và đẩy job trích xuất OCR bất đồng bộ vào hàng đợi River Queue.
- **_Chi tiết Chức năng:_**
  - **Kiểm tra Ảnh:** Chấp nhận 1–5 ảnh (JPEG, PNG, HEIC) dung lượng tối đa 10MB mỗi ảnh. Kiểm tra magic bytes.
  - **Kiểm tra Trạng thái Nhóm:** Kiểm tra trước nhóm chưa bị khóa nộp hóa đơn (`409 BILL_SUBMISSION_LOCKED`) và người gọi là thành viên đang hoạt động.
  - **Tải lên & Đẩy Hàng đợi:**
    - Tải ảnh lên bộ lưu trữ Cloudinary.
    - Trong một transaction cơ sở dữ liệu duy nhất: chèn bản ghi `bills` ở trạng thái `draft` (version = 1, người gọi làm `creditor`), chèn metadata ảnh, và đẩy job River `bill_ocr` chứa `bill_id`.
    - Trả về HTTP `202 Accepted` kèm entity hóa đơn nháp ban đầu.
  - **Worker Trích xuất:**
    - Worker River nhận job; ghép các ảnh nhiều trang theo chiều dọc; thu nhỏ chiều rộng > 1200px (JPEG 90); gọi LlamaExtract (timeout 8s).
    - Nếu gặp lỗi cấu trúc vĩnh viễn, đánh dấu job `failed` và không thử lại. Nếu gặp lỗi mạng tạm thời, thử lại với exponential backoff tối đa `BILL_OCR_MAX_ATTEMPTS` (mặc định 3 lần).
    - Thành công: lưu JSONB dữ liệu ứng viên trích xuất và phát sự kiện `ocr.updated` (status: `succeeded`).
    - Thất bại: cập nhật trạng thái job thành `failed` và phát sự kiện `ocr.updated` (status: `failed`).
  - **Xem xét phía Client:** Client nhận sự kiện `ocr.updated` trên luồng SSE của người dùng, hiển thị modal xem xét dữ liệu trích xuất, và áp dụng dữ liệu qua merge cục bộ kèm gọi `PUT /bills/{id}` có kiểm tra phiên bản.

---

#### **_4.1.18 - Cập nhật Hóa đơn Nháp & Phân bổ Mục (Update Draft Bill & Assignments)_** {#4118---cập-nhật-hóa-đơn-nháp--phân-bổ-mục-update-draft-bill--assignments}

- **_Kích hoạt Chức năng:_** Chủ nợ hoặc Trưởng nhóm cập nhật thông tin hóa đơn, danh sách món, phụ phí, hoặc phân bổ người tham gia (`PUT /api/v1/bills/{id}`).
- **_Mô tả Chức năng:_** Chỉnh sửa nội dung hóa đơn nháp và phân bổ món dưới cơ chế khóa phiên bản lạc quan.
- **_Chi tiết Chức năng:_**
  - **Quyền hạn & Trạng thái:** Người gọi phải là Trưởng nhóm hoặc Chủ nợ. Hóa đơn phải ở trạng thái `draft` hoặc `reviewed`. Nếu hóa đơn đang ở trạng thái `reviewed`, việc lưu chỉnh sửa sẽ **tự động giáng trạng thái trở lại `draft`** và xóa các mốc thời gian kiểm tra trước đó.
  - **Khóa Lạc quan:** Yêu cầu phải gửi kèm `version` hiện tại. Máy chủ kiểm tra `WHERE id = $1 AND version = $2`. Nếu phiên bản không khớp, trả về HTTP `409 VERSION_CONFLICT`.
  - **Tính toán & Suy diễn:**
    - Giá sau giảm của từng món: $\text{final\_price} = \text{line\_total} - \text{item\_discount}$.
    - Tổng tiền phân bổ: $\text{allocTotal} = \sum \text{final\_price} + \text{service\_charge} + \text{vat} - \text{general\_discount}$. (Trường `total` do client/OCR gửi lên bị bỏ qua để tránh sai lệch làm tròn).
    - Phân bổ món gán cho các thành viên với trọng số dương (được nhân tỷ lệ $10^8$ dùng số học hữu tỉ `big.Rat`).

---

#### **_4.1.19 - Kiểm tra Hóa đơn (Review Bill)_** {#4119---kiểm-tra-hóa-đơn-review-bill}
- **_Kích hoạt Chức năng:_** Chủ nợ hoặc Trưởng nhóm xác nhận hóa đơn đã sẵn sàng để kiểm tra (`POST /api/v1/bills/{id}/review`).
- **_Mô tả Chức năng:_** Chạy thử nghiệm phân bổ và kiểm tra toàn bộ điều kiện chặn trước khi chuyển sang trạng thái đã kiểm tra.
- **_Chi tiết Chức năng:_**
  - **Kiểm tra Điều kiện Chặn:** Đánh giá xem hóa đơn có: món chưa phân bổ (`ITEM_UNASSIGNED`), thành viên đã rời nhóm được gán món (`INACTIVE_MEMBER_ASSIGNED`), thiếu chủ nợ (`CREDITOR_REQUIRED`), giảm giá vượt quá tổng tiền (`DISCOUNT_EXCEEDS_BILL`), tổng tiền các món không khớp tạm tính ($\text{subtotal} \neq \sum \text{line\_totals}$), hoặc sai lệch tổng tiền cuối cùng.
  - **Phản hồi:** Nếu có bất kỳ điều kiện nào vi phạm, trả về HTTP `422 BILL_NOT_READY`. (Các mã lỗi cụ thể được trả về trong `mismatch_codes` khi gọi `GET /bills/{id}`). Nếu hợp lệ hoàn toàn, chuyển trạng thái hóa đơn sang `reviewed` và tăng phiên bản `version`.

---

#### **_4.1.20 - Chốt Hóa đơn (Finalize Bill)_** {#4120---chốt-hóa-đơn-finalize-bill}

- **_Kích hoạt Chức năng:_** Trưởng nhóm chốt một hóa đơn đã được kiểm tra (`POST /api/v1/bills/{id}/finalize`).
- **_Mô tả Chức năng:_** Tính toán phần tiền dứt khoát của từng người tham gia theo thuật toán làm tròn số dư lớn nhất Hamilton, khóa bất biến hóa đơn, tạo các bản ghi công nợ chờ thanh toán, và gửi thông báo đẩy.
- **_Chi tiết Chức năng:_**
  - **Ủy quyền & Bất biến:** Người gọi bắt buộc phải là Trưởng nhóm (`403 FORBIDDEN` nếu không phải). Chủ nợ phải có tài khoản ngân hàng hợp lệ đã thiết lập (`422 BANK_ACCOUNT_REQUIRED`). Yêu cầu phải gửi kèm `version` và `Idempotency-Key`.
  - **Ghi chú Phân kỳ Bản phát hành:** V1 hoạt động dưới quyền chốt trực tiếp của Trưởng nhóm; cơ chế con nợ bỏ phiếu đồng thuận ([Spec 0007](specs/0007-debtor-bill-consent/index.md)) là nâng cấp dành cho V2 và không chặn việc chốt hóa đơn trong V1.
  - **Thuật toán Phân chia Tiền (Phương pháp Số dư Lớn nhất / Thuật toán Hamilton):**
    1. Tính toán phần tiền hữu tỉ chính xác (`big.Rat`) cho từng người tham gia dựa trên trọng số món và tỷ lệ phụ phí/giảm giá (Mô hình phân bổ chính xác Migration 000016).
    2. Giới hạn giảm giá của người không phải chủ nợ để phần tiền cá nhân không bị âm (khoản giảm giá thừa sẽ chuyển sang cho chủ nợ).
    3. Lấy phần nguyên VNĐ của từng người tham gia ($\lfloor \text{share}_i \rfloor$).
    4. Tính số dư còn lại: $R = \text{allocTotal} - \sum \lfloor \text{share}_i \rfloor$ (trong đó $0 \le R < N$).
    5. Cộng thêm $+1$ VNĐ cho $R$ người tham gia có **phần thập phân dư lớn nhất**. Nếu phần dư bằng nhau, phá vỡ thế hòa dựa trên **thứ tự UUID thành viên tăng dần** (hoàn toàn khách quan, không thiên vị chủ nợ).
    6. Kiểm tra lại $\sum \text{final\_shares} = \text{allocTotal}$ chính xác tuyệt đối tới 1 VNĐ.
  - **Thực thi Transaction:**
    - Chuyển trạng thái hóa đơn sang `finalized`.
    - Chèn bản ghi snapshot bất biến `bill_shares` cho toàn bộ người tham gia.
    - Chèn bản ghi `debts` ở trạng thái `awaiting` cho mỗi người tham gia không phải chủ nợ có $\text{amount} > 0$ (mỗi cặp con nợ - chủ nợ chỉ có một khoản nợ duy nhất cho hóa đơn này).
    - Đẩy job River `send_notification` và ghi nhật ký hoạt động nhóm. Trả về HTTP 200.

---

#### **_4.1.21 - Chốt Hàng loạt Tất cả Hóa đơn (Batch Finalize-All Bills)_** {#4121---chốt-hàng-loạt-tất-cả-hóa-đơn-batch-finalize-all-bills}

- **_Kích hoạt Chức năng:_** Trưởng nhóm bắt đầu đợt quyết toán toàn bộ nhóm (`POST /api/v1/groups/{id}/bills/finalize-all`).
- **_Mô tả Chức năng:_** Tự động khóa nộp hóa đơn và xử lý chốt toàn bộ các hóa đơn nháp/đã kiểm tra qua các job hàng đợi song song.
- **_Chi tiết Chức năng:_**
  - Người gọi phải là Trưởng nhóm. Tự động đặt `groups.bill_submission_locked_at = now()`.
  - Chặn nếu đang có một đợt chốt hàng loạt khác đang chạy (`409 BULK_FINALIZE_IN_PROGRESS`).
  - Tạo bản ghi `group_bill_finalize_batches` và chụp nhanh ID cùng phiên bản của toàn bộ hóa đơn `draft` và `reviewed` vào bảng `group_bill_finalize_items`.
  - Đẩy một job River `bill_bulk_finalize_item` cho mỗi mục hóa đơn.
  - **Transaction Độc lập cho Từng Job:** Mỗi hóa đơn được xử lý trong một transaction cơ sở dữ liệu riêng biệt. Nếu một hóa đơn cụ thể bị lỗi (ví dụ: món chưa phân bổ), mục đó sẽ được đánh dấu `failed` kèm lý do lỗi, trong khi tất cả các hóa đơn hợp lệ khác vẫn được chốt thành công.
  - Khi xử lý xong tất cả các mục, cập nhật trạng thái đợt thành `completed` và gửi thông báo cho Trưởng nhóm.

---

#### **_4.1.22 - Hủy bỏ Hóa đơn Đã chốt (Void Finalized Bill)_** {#4122---hủy-bỏ-hóa-đơn-đã-chốt-void-finalized-bill}

- **_Kích hoạt Chức năng:_** Trưởng nhóm hủy một hóa đơn đã chốt bị sai sót (`POST /api/v1/bills/{id}/void`).
- **_Mô tả Chức năng:_** Hủy một hóa đơn đã chốt bất biến và các khoản nợ chờ thanh toán liên quan.
- **_Chi tiết Chức năng:_**
  - **Kiểm tra Hợp lệ:** Người gọi phải là Trưởng nhóm; hóa đơn phải ở trạng thái `finalized`; yêu cầu gửi kèm `version` và `reason` bắt buộc (1–500 ký tự).
  - **Kiểm soát Trạng thái Thanh toán:** Lấy khóa trên nhóm, hóa đơn và các khoản nợ theo thứ tự UUID tăng dần. Nếu có bất kỳ khoản nợ liên quan nào không còn ở trạng thái `awaiting` hoặc đã có `payment_id` (nghĩa là đã nộp bằng chứng chuyển khoản), việc hủy hóa đơn bị từ chối với HTTP `409 PAYMENT_ALREADY_STARTED`.
  - **Xử lý Intent QR:** Nếu chỉ tồn tại intent mã QR chưa nộp bằng chứng (`pending_proof`), hệ thống tự động đánh dấu intent thanh toán là `superseded` và cho phép thực hiện thao tác hủy.
  - **Thực thi:** Cập nhật `bills.status = 'voided'`, chuyển toàn bộ các khoản nợ liên quan sang `status = 'voided'`, ghi nhật ký hoạt động `voided_bill`, và phát sự kiện realtime `bill.voided`. Trả về HTTP 200.

---

#### **_4.1.23 - Xem Phân bổ Chi phí & Chi tiết Nhóm (View Allocated Expense & Breakdown)_** {#4123---xem-phân-bổ-chi-phí--chi-tiết-nhóm-view-allocated-expense--breakdown}

- **_Kích hoạt Chức năng:_** Thành viên nhóm xem một hóa đơn hoặc bảng tổng kết tài chính nhóm.
- **_Mô tả Chức năng:_** Hiển thị chi tiết từng món được phân bổ, phân chia thuế/phụ phí theo tỷ lệ, điều chỉnh làm tròn và số dư ròng của các thành viên.
- **_Chi tiết Chức năng:_**
  - Hiển thị danh sách các món ăn kèm số lượng mà thành viên tham gia.
  - Thể hiện rõ ràng phụ phí dịch vụ, thuế VAT, giảm giá theo tỷ lệ và khoản điều chỉnh làm tròn cá nhân `rounding_adjustment` (+1/0 VNĐ).
  - Hiển thị số dư chung của nhóm ($+\text{phải thu} / -\text{phải trả}$) và danh sách các khoản nợ đang mở.

---

#### **_4.1.24 - Tạo VietQR Thanh toán (Generate Payment QR)_** {#4124---tạo-vietqr-thanh-toán-generate-payment-qr}

- **_Kích hoạt Chức năng:_** Con nợ chọn một hoặc nhiều khoản nợ cần thanh toán (`POST /api/v1/groups/{id}/payments/qr`).
- **_Mô tả Chức năng:_** Tạo mã VietQR động chuẩn chứa thông tin tài khoản ngân hàng người nhận, tổng số tiền chính xác và mã tham chiếu duy nhất.
- **_Chi tiết Chức năng:_**
  - **Idempotency & Gộp Nợ:** Yêu cầu header `Idempotency-Key` dạng UUIDv5 tất định (tạo từ `groupId + creditorId + sortedDebtIds`). Danh sách nợ nhận từ 1–100 debt ID (hoặc bỏ trống để tự động gộp tất cả các khoản nợ `awaiting` đối với chủ nợ đó).
  - **Kiến trúc Intent Không Khóa Nợ:** Việc tạo mã QR **không khóa các khoản nợ** (`debts` vẫn giữ nguyên trạng thái `awaiting`, `payment_id` vẫn là NULL). Trưởng nhóm vẫn có thể hủy hóa đơn khi mã QR đang mở.
  - **Thực thi:**
    - Xác nhận tất cả các khoản nợ đã chọn thuộc về người gọi (với vai trò con nợ) và người nhận (với vai trò chủ nợ), và đang ở trạng thái `awaiting` (`409 DEBTS_NOT_AWAITING` nếu không khớp).
    - Xác nhận Chủ nợ đã cấu hình tài khoản ngân hàng hợp lệ (`422 BANK_ACCOUNT_REQUIRED`).
    - Tạo mới hoặc tái sử dụng bản ghi thanh toán `pending_proof` kèm `reference_code` duy nhất (`PAY` + 8 ký tự Base32, loại bỏ các ký tự dễ nhầm lẫn `I`, `O`, `0`, `1`).
    - Xây dựng payload VietQR và trả về entity thanh toán chứa `qr_image_url`. (Ảnh VietQR được render động theo mẫu compact NAPAS mà không giữ tiền của người dùng).

---

#### **_4.1.25 - Nộp Bằng chứng Chuyển khoản (Submit Payment Proof)_** {#4125---nộp-bằng-chứng-chuyển-khoản-submit-payment-proof}

- **_Kích hoạt Chức năng:_** Con nợ đính kèm ảnh chụp màn hình chuyển khoản sau khi chuyển tiền qua ngân hàng (`POST /api/v1/groups/{id}/payments/{pid}/proof`).
- **_Mô tả Chức năng:_** Tải ảnh bằng chứng thanh toán lên và khóa các khoản nợ ở trạng thái `pending_confirmation` chờ chủ nợ xác nhận.
- **_Chi tiết Chức năng:_**
  - **Quy trình Thực thi 2 Pha:**
    - **Pha 1 (`PrepareProof`):** Xác thực người gọi là con nợ; xác nhận thanh toán ở trạng thái `pending_proof` (`409 PAYMENT_NOT_PENDING_PROOF` nếu sai); giữ trước khóa chống trùng lặp ở trạng thái `in_progress` kèm `operation_id` UUIDv7. Tải ảnh chụp màn hình (JPEG/PNG/HEIC tối đa 10MB) lên Cloudinary (`payments/{pid}/proofs/{operationId}`).
    - **Pha 2 (`SubmitProof`):** Khóa các khoản nợ liên quan; xác nhận toàn bộ các khoản nợ vẫn ở trạng thái `awaiting`; chụp nhanh thông tin ngân hàng hiện tại của chủ nợ vào bản ghi thanh toán; chuyển trạng thái thanh toán sang `pending_confirmation`, đồng thời cập nhật `debts.status = 'pending_confirmation'` và `debts.payment_id = pid`.
  - **Xử lý Đền bù:** Nếu Pha 2 thất bại (ví dụ: hóa đơn vừa bị hủy đồng thời), máy chủ xóa ảnh đã tải trên Cloudinary (hoặc đẩy job dọn dẹp) và đặt lại lượt nộp bằng chứng với ID thao tác mới.
  - Phát sự kiện `settlement.payment_changed`, ghi nhật ký hoạt động và gửi thông báo đẩy cho Chủ nợ.

---

#### **_4.1.26 - Xác nhận hoặc Từ chối Thanh toán (Confirm / Reject Payment)_** {#4126---xác-nhận-hoặc-từ-chối-thanh-toán-confirm--reject-payment}

- **_Kích hoạt Chức năng:_** Chủ nợ kiểm tra bằng chứng thanh toán đang chờ duyệt (`POST /api/v1/groups/{id}/payments/{pid}/confirm`, `POST /api/v1/groups/{id}/payments/{pid}/reject`).
- **_Mô tả Chức năng:_** Xác nhận đã nhận tiền (chuyển nợ sang đã quyết toán) hoặc từ chối bằng chứng không hợp lệ (hoàn trả nợ về chờ thanh toán).
- **_Chi tiết Chức năng:_**
  - **Ủy quyền:** Người gọi **bắt buộc phải là Chủ nợ** của khoản thanh toán (`403 FORBIDDEN` nếu là Trưởng nhóm hoặc thành viên khác). Yêu cầu gửi kèm `Idempotency-Key` (UUIDv5).
  - **Luồng Xác nhận (Confirm):**
    - Trong một transaction duy nhất: xác nhận thanh toán đang ở trạng thái `pending_confirmation`; chuyển trạng thái thanh toán sang `confirmed` (`confirmed_at = now()`); chuyển tất cả các khoản nợ liên kết sang `settled` (`settled_at = now()`).
    - Ghi nhật ký hoạt động `confirmed_payment`; gửi thông báo đẩy cho Con nợ; phát sự kiện `home.balance_changed` cho cả con nợ và chủ nợ.
  - **Luồng Từ chối (Reject):**
    - Bắt buộc phải có lý do `reason` (1–500 runes, `400 VALIDATION_FAILED` nếu để trống).
    - Trong một transaction duy nhất: chuyển trạng thái thanh toán sang `rejected` (lưu lại để phục vụ đối soát kiểm toán); hoàn trả tất cả các khoản nợ liên kết về trạng thái `awaiting` và xóa `payment_id = NULL`.
    - Ghi nhật ký hoạt động `rejected_payment`; gửi thông báo đẩy cho Con nợ kèm lý do từ chối; cho phép Con nợ tạo mã QR mới để thanh toán lại.

---

#### **_4.1.27 - Nhắc nợ Thủ công & Trình quét Tự động (Debt Reminders & Auto Scan)_** {#4127---nhắc-nợ-thủ-công--trình-quét-tự-động-debt-reminders--auto-scan}

- **_Kích hoạt Chức năng:_** Chủ nợ/Trưởng nhóm kích hoạt nhắc nợ thủ công hoặc worker định kỳ chạy nền.
- **_Mô tả Chức năng:_** Gửi thông báo cho các khoản nợ chưa thanh toán và các khoản xác nhận thanh toán bị tắc nghẽn.
- **_Chi tiết Chức năng:_**
  - **Nhắc nợ Thủ công (`POST /groups/{id}/debts/{did}/remind`):**
    - Người gọi phải là Chủ nợ hoặc Trưởng nhóm; khoản nợ phải ở trạng thái `awaiting`.
    - Sử dụng `Idempotency-Key` ngẫu nhiên dạng UUIDv4.
    - Giới hạn tần suất: **tối đa 3 lần nhắc cho mỗi khoản nợ, mỗi lần cách nhau $\ge 24$ giờ** (`429 REMINDER_RATE_LIMITED`).
    - Tăng `reminder_count`, ghi nhật ký hoạt động `payment_reminder`, và gửi thông báo đẩy cho Con nợ. Giao diện app client hiển thị bộ đếm ngược thời gian chờ 24 giờ.
  - **Trình quét Tự động (Job River `settlement_scan`):**
    - Chạy định kỳ mỗi giờ (và khi khởi động server).
    - Quét các khoản nợ ở trạng thái `awaiting` tạo quá 72 giờ có `reminder_count < 3` và lần nhắc gần nhất cách đây $\ge 24$ giờ bằng cú pháp `SKIP LOCKED LIMIT 100`. Gửi thông báo nhắc nhở tự động từ hệ thống (dùng chung hạn mức tối đa 3 lần).
    - Quét các khoản thanh toán ở trạng thái `pending_confirmation` tạo quá 48 giờ có `stalled_alerted_at IS NULL`. Gửi cảnh báo xác nhận tồn đọng một lần duy nhất tới Chủ nợ mà không tự động gạch nợ.

---

#### **_4.1.28 - Thông báo Trong ứng dụng & Push Notification (In-App & Push Notifications)_** {#4128---thông-báo-trong-ứng-dụng--push-notification-in-app--push-notifications}

- **_Kích hoạt Chức năng:_** Các sự kiện hệ thống tạo ra thông báo (`GET /api/v1/notifications`, `PATCH /api/v1/notifications/{id}/read`).
- **_Mô tả Chức năng:_** Điều phối nhật ký thông báo trong ứng dụng, bộ đếm chưa đọc, gửi thông báo đẩy FCM và điều hướng deep linking.
- **_Chi tiết Chức năng:_**
  - **Tính Nhất quán Giao dịch:** Bản ghi thông báo trong ứng dụng và job River `send_notification` luôn được đẩy vào hàng đợi **trong cùng một transaction cơ sở dữ liệu** với hành động nghiệp vụ gốc (chốt hóa đơn, nộp bằng chứng, xác nhận, nhắc nợ).
  - **Worker Đẩy FCM:** Worker River nhận job `send_notification`, lấy FCM token phiên hoạt động mới nhất của người nhận (`ORDER BY issued_at DESC`), và gửi thông báo đẩy. Các token không hợp lệ hoặc đã bị hủy đăng ký sẽ được tự động xóa khỏi cơ sở dữ liệu (`ClearFCMToken`).
  - **Trung tâm Thông báo Trong ứng dụng:** Cung cấp lịch sử thông báo phân trang (`GET /notifications`), đánh dấu đã đọc đơn lẻ hoặc hàng loạt một cách lạc quan (`PATCH /notifications/{id}/read`, `PATCH /notifications/read-all`), và huy hiệu đếm số lượng chưa đọc.
  - **Bộ Phân giải Deep Linking:** Điều hướng khi người dùng nhấn vào thông báo dựa trên bộ phân giải ưu tiên loại (`NotificationRouteResolver`), ưu tiên chuyển thẳng đến các tab Chi tiết Nhóm (`/groups/:id`) bất cứ khi nào có `group_id`.

---

#### **_4.1.29 - Quản trị Hệ thống & Cổng Web Admin (Admin Management & Web Portal)_** {#4129---quản-trị-hệ-thống--cổng-web-admin-admin-management--web-portal}

- **_Kích hoạt Chức năng:_** Quản trị viên hệ thống truy cập Cổng Web Admin nhúng (`/admin-portal/`, `GET/PUT /api/v1/admin/*`).
- **_Mô tả Chức năng:_** Cung cấp khả năng quan sát nền tảng, các probe sức khỏe hệ thống và kiểm duyệt tài khoản.
- **_Chi tiết Chức năng:_**
  - **Cổng Web Nhúng:** Nhúng trực tiếp vào file nhị phân Go (`//go:embed` trong `web/web.go`) và phục vụ tại đường dẫn `/admin-portal/`.
  - **Quản lý Tài khoản (`GET /admin/accounts`, `GET /admin/accounts/{id}`):** Tìm kiếm phân trang (email, tên, số điện thoại) và lọc theo trạng thái/vai trò; hiển thị hồ sơ người dùng an toàn với số tài khoản ngân hàng bị che mặt nạ (`******` + 4 số cuối) cùng số lượng nợ/có đang mở.
  - **Cập nhật Trạng thái (`PUT /admin/accounts/{id}/status`):**
    - Cho phép chuyển đổi trạng thái tài khoản sang `active`, `suspended`, hoặc `locked` kèm lý do kiểm toán bắt buộc.
    - Ngăn chặn tự chỉnh sửa tài khoản của chính mình (`403 CANNOT_MODIFY_SELF`) và tạm khóa quản trị viên khác (`403 CANNOT_MODIFY_ADMIN`).
    - **Thu hồi Phiên Tức thì:** Việc tạm khóa hoặc khóa vĩnh viễn tài khoản sẽ ngay lập tức thu hồi toàn bộ các phiên hoạt động (`admin_suspended` / `admin_locked`), vô hiệu hóa refresh token, và phát sự kiện SSE `session.ended` trong cùng một transaction.
    - Cảnh báo quản trị viên nếu tài khoản mục tiêu còn công nợ chưa quyết toán mà không chặn thao tác.
    - Ghi các bản ghi bất biến vào bảng `admin_audit_logs`.
  - **Tổng quan Hệ thống & Probe Sức khỏe:** `GET /admin/system/overview` cung cấp số lượng thời gian thực về người dùng, nhóm, hóa đơn, công nợ và độ sâu hàng đợi River; `/health/ready` kiểm tra kết nối cơ sở dữ liệu và sức khỏe của listener (trả về HTTP 503 degraded nếu listener bị ngắt kết nối).

---

#### **_4.1.30 - Kiến trúc Realtime Sự kiện Hợp nhất (Unified Realtime Architecture)_** {#4130---kiến-trúc-realtime-sự-kiện-hợp-nhất-unified-realtime-architecture}

- **_Kích hoạt Chức năng:_** Client mở luồng SSE duy trì sau khi đăng nhập (`GET /api/v1/users/me/events`).
- **_Mô tả Chức năng:_** Đa hợp các sự kiện hệ thống thời gian thực qua một kết nối duy nhất cho mỗi phiên bằng cơ chế `LISTEN/NOTIFY` của PostgreSQL.
- **_Chi tiết Chức năng:_**
  - **Listener Dùng chung Duy nhất:** Một kết nối PostgreSQL duy nhất trên mỗi instance backend lắng nghe các kênh `bill_events`, `group_events`, và `user_events`.
  - **Trọng tài Thay thế Kết nối:** Các luồng kết nối đồng thời trên cùng một phiên được phân xử dựa trên thứ tự commit `NOTIFY` của PostgreSQL (`stream.replace`), đảm bảo luôn có chính xác một luồng hoạt động cho mỗi phiên.
  - **Payload Vô hiệu hóa Gọn nhẹ:** Máy chủ phát các mô tả sự kiện dung lượng nhẹ (`ready`, `invalidate`, `roster`, `ocr.updated`, `heartbeat`, `close`).
  - **Sổ đăng ký Mối quan tâm phía Client:** Client Flutter đăng ký các màn hình hiển thị đang hoạt động (`home.groups`, `groups.index`, `group.bills`, `group.debts`, v.v.), gom cụm các kích hoạt làm mới trong cửa sổ 250ms (debounce), và thực hiện vá danh sách tại chỗ cho các thẻ nhóm (`patchGroup`) mà không bị reset phân trang.

---

#### **_4.1.31 - Bắt kịp Đồng bộ Delta Nhóm (Group Delta Catch-up Synchronization)_** {#4131---bắt-kịp-đồng-bộ-delta-nhóm-group-delta-catch-up-synchronization}

- **_Kích hoạt Chức năng:_** Ứng dụng di động kết nối lại sau khi rớt mạng hoặc phát hiện khoảng cách phiên bản trong các sự kiện nhóm nhận được (ví dụ: phiên bản client hiện tại là 10 nhưng sự kiện nhận được là 12) qua `GET /api/v1/groups/{id}/sync?since=N`.
- **_Mô tả Chức năng:_** Cung cấp các delta sự kiện có thứ tự, không bị đứt đoạn từ bảng `group_events`, cho phép tái tạo trạng thái client mà không cần tải lại toàn bộ màn hình tốn kém.
- **_Chi tiết Chức năng:_**
  - **Đánh số Phiên bản Tăng Đơn điệu:** Mọi đột biến dữ liệu nhóm đều tăng `groups.roster_version` dưới khóa mức hàng (`LockActiveGroup`) và chèn vào bảng `group_events (group_id, version, event_type, payload, created_at)`.
  - **Truy vấn & Lấy Delta:** Khi nhận yêu cầu `?since=N`, trả về tất cả các sự kiện có `version > N` theo thứ tự tăng dần (`ORDER BY version ASC`).
  - **Dự phòng Khôi phục Toàn diện (Snapshot Fallback):** Nếu phiên bản `since` của client cũ hơn cửa sổ lưu trữ log được cấu hình (hoặc nếu log nhóm đã bị dọn bớt), máy chủ trả về HTTP 200 kèm `reset_required: true`, yêu cầu client tải lại toàn bộ chi tiết nhóm (`GET /api/v1/groups/{id}`).
  - **Xử lý phía Client:** Repository Riverpod của Flutter áp dụng tuần tự các delta sự kiện bị bỏ lỡ vào cache cục bộ, khôi phục trạng thái một cách liền mạch.

---

### **4.2 Các Chức năng Tự động của Hệ thống** {#42-các-chức-năng-tự-động-của-hệ-thống}

#### **_4.2.1 - Tác vụ Ngầm Tự động & Hàng đợi River Queue Worker_** {#421---tác-vụ-ngầm-tự-động--hàng-đợi-river-queue-worker}

- **_Kích hoạt Chức năng:_** Động cơ River Queue và các bộ đếm thời gian (tickers) chạy nền.
- **_Mô tả Chức năng:_** Thực thi các khối lượng công việc bất đồng bộ một cách đáng tin cậy trên nền PostgreSQL mà không cần Redis hay message broker bên ngoài.
- **_Các Worker Hàng đợi & Lịch trình:_**
  1. **Worker `bill_ocr`:** Ghép nối ảnh hóa đơn nhiều trang bất đồng bộ, gọi Vision LLM OCR, thử lại lỗi mạng tạm thời (tối đa 3 lần), và phát sự kiện hoàn tất `ocr.updated`.
  2. **Worker `bill_bulk_finalize_item`:** Xử lý chốt từng hóa đơn đơn lẻ trong các đợt chốt hàng loạt trong các transaction cơ sở dữ liệu độc lập, cô lập lỗi.
  3. **Worker `send_notification`:** Đẩy thông báo FCM bất đồng bộ tới token thiết bị, tự động dọn dẹp các token đã bị hủy đăng ký.
  4. **Worker `settlement_scan`:** Cron job hàng giờ quét các khoản nợ chờ thanh toán tồn đọng 72 giờ (gửi thông báo nhắc nợ tự động) và các khoản bằng chứng thanh toán chờ xác nhận tồn đọng 48 giờ (cảnh báo chủ nợ).
  5. **Các Ticker Dọn dẹp Auth & Media:** Ticker chạy nền mỗi 24 giờ dọn dẹp token/phiên hết hạn; ticker mỗi 60 giây thử lại các tác vụ xóa ảnh đại diện Cloudinary mồ côi từ bảng `media_cleanup_jobs`.

---

## **5. Yêu cầu Phi chức năng** {#5-yêu-cầu-phi-chức-năng}

### **5.1. Giao diện Bên ngoài** {#51-giao-diện-bên-ngoài}

#### **_5.1.1 Giao diện Người dùng (User Interface)_** {#511-giao-diện-người-dùng-user-interface}

- **UI-01:** Ứng dụng di động Flutter đa nền tảng (iOS & Android) hỗ trợ giao diện responsive từ màn hình 5.0" đến 10.0" mà không bị cuộn ngang trên các luồng chia tiền/thanh toán quan trọng.
- **UI-02:** Cổng Web Admin nhúng (`/admin-portal/`) tương thích responsive trên các trình duyệt máy tính (Chrome, Firefox, Safari, Edge), hỗ trợ biểu đồ dashboard, bảng người dùng và các modal thao tác.
- **UI-03:** Mã VietQR chuẩn được hiển thị động với độ phân giải tối thiểu $250 \times 250$ pixel kèm thông tin có thể sao chép: mã ngân hàng, số tài khoản, số tiền và mã tham chiếu.
- **UI-04:** Phản hồi người dùng tức thì: tự động nộp mã OTP Pinput khi nhập đủ 6 số, bộ đếm ngược thời gian chờ 60s/24h, trạng thái đã đọc thông báo in-app lạc quan, và cập nhật danh sách realtime có debounce.

#### **_5.1.2 Giao diện Phần mềm (Software Interface)_** {#512-giao-diện-phần-mềm-software-interface}

- **SI-01:** Backend API xây dựng bằng Go 1.24+ sử dụng Chi router, giao tiếp qua HTTPS/TLS 1.2+ với định dạng phản hồi JSON chuẩn hóa (`success`, `data`, `error`).
- **SI-02:** Cơ sở dữ liệu PostgreSQL 18 truy cập qua `pgxpool` và các truy vấn sinh mã an toàn kiểu (type-safe) bằng `sqlc` cùng migration Goose SQL.
- **SI-03:** Xử lý tác vụ ngầm bất đồng bộ qua River Queue (`github.com/riverqueue/river`) chạy trực tiếp trên PostgreSQL.
- **SI-04:** Tích hợp Vision OCR với LlamaExtract / Gemini Flash qua HTTPS.
- **SI-05:** Tích hợp lưu trữ đám mây với Cloudinary API cho hóa đơn, ảnh đại diện và ảnh chụp màn hình bằng chứng thanh toán.
- **SI-06:** Thông báo đẩy gửi qua Firebase Cloud Messaging (FCM) HTTP v1 API.
- **SI-07:** Email giao dịch gửi qua Gmail SMTP (TLS).

#### **_5.1.3 Giao diện Phần cứng (Hardware Interface)_** {#513-giao-diện-phần-cứng-hardware-interface}

- **HI-01:** Camera sau của thiết bị di động ($\ge 5\text{MP}$) dùng để chụp ảnh hóa đơn và quét mã QR mời nhóm.
- **HI-02:** Độ phân giải màn hình tối thiểu $720 \times 1280$ pixel để hiển thị rõ ràng mã VietQR có thể quét được.
- **HI-03:** Kết nối Internet Wi-Fi hoặc 4G/5G đang hoạt động.
- **HI-04:** Tối thiểu 100MB dung lượng lưu trữ trống trên thiết bị để cài đặt ứng dụng và lưu bộ nhớ đệm ảnh.

---

### **5.2. Thuộc tính Chất lượng** {#52-thuộc-tính-chất-lượng}

#### **_5.2.1 Hiệu năng & Khả năng Mở rộng (Performance & Scalability)_** {#521-hiệu-năng--khả-năng-mở-rộng-performance--scalability}

- **PERF-01:** Thời gian phản hồi máy chủ cho các API CRUD tiêu chuẩn $\le 200\text{ms}$ ở mức p95 dưới tải thông thường.
- **PERF-02:** Thời gian xử lý trích xuất OCR hóa đơn bất đồng bộ hoàn tất trong $\le 10\text{s}$ ở mức p90.
- **PERF-03:** Thời gian tạo payload VietQR và mã tham chiếu phía server $\le 50\text{ms}$.
- **PERF-04:** Thời gian tính toán phân bổ và làm tròn Hamilton hoàn tất trong $\le 20\text{ms}$ đối với hóa đơn 100 món và nhóm 50 thành viên.
- **PERF-05:** Kiến trúc realtime tối ưu kết nối hỗ trợ $\ge 10,000$ kết nối SSE người dùng đồng thời trên mỗi instance sử dụng một kết nối `LISTEN` PostgreSQL dùng chung duy nhất.
- **PERF-06:** Sức chứa nhóm giới hạn tối đa 50 thành viên hoạt động và 100 món trên mỗi hóa đơn.

#### **_5.2.2 Độ tin cậy & Tính Bền vững (Reliability & Robustness)_** {#522-độ-tin-cậy--tính-bền-vững-reliability--robustness}

- **REL-01:** Bất biến toán học nghiêm ngặt: mọi tính toán tiền tệ sử dụng số nguyên 64-bit (`int64` VNĐ) và số hữu tỉ chính xác (`big.Rat`); thuật toán làm tròn số dư lớn nhất Hamilton đảm bảo $\sum \text{shares} = \text{bill\_total}$ tuyệt đối không phát sinh sai lệch làm tròn.
- **REL-02:** Hóa đơn đã chốt mang tính bất biến tuyệt đối; các điều chỉnh yêu cầu phải hủy hóa đơn kèm lý do kiểm toán bắt buộc và tạo hóa đơn thay thế (`replaces_bill_id`).
- **REL-03:** Tính nhất quán giao dịch của tác vụ: các job River Queue (`send_notification`, `bill_ocr`) được đẩy vào hàng đợi bên trong cùng một transaction cơ sở dữ liệu với thao tác nghiệp vụ (BeforeCommit hooks).
- **REL-04:** Mọi thao tác hủy bỏ và thay đổi trạng thái tài chính đều được ghi vào các bảng nhật ký hoạt động chỉ ghi thêm (`group_activities`, `admin_audit_logs`).

#### **_5.2.3 Bảo mật & Quyền riêng tư (Security & Privacy)_** {#523-bảo-mật--quyền-riêng-tư-security--privacy}

- **SEC-01:** Logic phân quyền được thực thi nghiêm ngặt tại tầng use case sử dụng cơ chế xác thực phiên `liveAuth`.
- **SEC-02:** Thực thi đơn phiên và Xoay vòng Refresh Token kèm phát hiện tái sử dụng; mật khẩu được băm bằng bcrypt (`DefaultCost = 10`); OTP và refresh token được lưu dưới dạng hash SHA-256.
- **SEC-03:** Chống dò quét: Các endpoint đăng nhập, gửi lại OTP và quên mật khẩu trả về phản hồi đồng nhất; các endpoint mời nhóm trả về 404 đồng nhất cho các mã không hợp lệ/hết hạn.
- **SEC-04:** Che mặt nạ số tài khoản ngân hàng (`******` + 4 số cuối) trên các màn hình quản trị; bộ lọc log tự động loại bỏ mật khẩu, token, số tài khoản đầy đủ và payload OCR thô.
- **SEC-05:** Tuân thủ không giữ tiền: PaySplit không giữ tiền người dùng và không trích nợ tự động, hoàn toàn nằm ngoài phạm vi cấp phép dịch vụ trung gian thanh toán theo Nghị định 52/2024/NĐ-CP.

#### **_5.2.4 Tính Giải thích được (Explainability)_** {#524-tính-giải-thích-được-explainability}

- **EXP-01:** Minh bạch chi phí hoàn toàn: mọi bảng chi tiết hóa đơn thể hiện rõ ràng tiền từng món, phụ phí dịch vụ, thuế VAT, giảm giá theo tỷ lệ và điều chỉnh làm tròn cá nhân (+1/0 VNĐ).
- **EXP-02:** Khả năng truy xuất nguồn gốc đầy đủ: mọi bản ghi công nợ đều liên kết trực tiếp tới hóa đơn cha, chủ nợ, con nợ và biên lai bằng chứng thanh toán liên quan.

#### **_5.2.5 Khả năng Bảo trì & Tái lập (Maintainability & Reproducibility)_** {#525-khả-năng-bảo-trì--tái-lập-maintainability--reproducibility}

- **MNT-01:** Kiến trúc Modular Monolith tuân thủ Clean Architecture (Domain $\leftarrow$ UseCase $\leftarrow$ Repository Interface $\leftarrow$ PostgreSQL Adapter / HTTP Delivery).
- **MNT-02:** Schema cơ sở dữ liệu được quản lý qua các file migration Goose tuần tự (`db/migrations/`) kèm sinh mã truy vấn an toàn kiểu bằng `sqlc`.
- **MNT-03:** Frontend di động cấu trúc theo Clean Architecture + Feature-First, sinh mã Riverpod, model bất biến Freezed và Injectable DI.

---

## **6. Tổng quan Kiến trúc (Cấp cao)** {#6-tổng-quan-kiến-trúc-cấp-cao}

### **6.1 Các Thành phần Hệ thống** {#61-các-thành-phần-hệ-thống}

- **_Tầng Ứng dụng Khách (Client Layer):_**
  - **Ứng dụng Di động Flutter:** Ứng dụng đa nền tảng cho iOS & Android xây dựng với quản lý trạng thái Riverpod, điều hướng GoRouter, client HTTP Dio và `flutter_secure_storage`.
  - **Cổng Web Admin Nhúng:** Dashboard quản trị trang đơn được nhúng trực tiếp vào file nhị phân Go (`//go:embed`) và phục vụ tại `/admin-portal/`.
- **_Tầng API & Phân phối (API & Delivery Layer):_**
  - **Chi HTTP Router:** Định tuyến RESTful module hóa với ngăn xếp middleware (RequestID, ClientIP, Prometheus Metrics, Logger, Recoverer, CORS, RateLimit, LiveAuth).
  - **Trình xử lý Realtime SSE Hợp nhất:** Xử lý luồng kết nối duy trì (`GET /api/v1/users/me/events`) quản lý việc đăng ký nhận sự kiện và cơ chế bắt tay thay thế kết nối.
- **_Các Module Nghiệp vụ Domain:_**
  - **Auth / User:** Định danh, đăng ký, xác thực email OTP, băm bcrypt, JWT Access Token, xoay vòng Refresh Token, tải ảnh đại diện Cloudinary, kiểm tra danh bạ ngân hàng.
  - **Group:** Vòng đời nhóm, mã mời Base62, sức chứa 50 thành viên, chuyển giao trưởng nhóm `NOWAIT`, hủy kích hoạt mềm, khóa nộp hóa đơn.
  - **Bill / OCR:** Xử lý ảnh hóa đơn, tích hợp OCR LlamaExtract, phân bổ món theo trọng số, khóa lạc quan `version`, tính toán chia tiền Hamilton, chốt đơn lẻ & hàng loạt, hủy hóa đơn.
  - **Settlement:** Tạo payload VietQR, intent mã QR không khóa nợ (`pending_proof`), nộp bằng chứng 2 pha, chủ nợ xác nhận/từ chối, nhắc nợ thủ công/tự động.
  - **Notification:** Trung tâm thông báo in-app, bộ đếm chưa đọc, gửi thông báo đẩy FCM, định tuyến deep link theo loại.
  - **Admin:** Kiểm duyệt trạng thái tài khoản (`active`, `suspended`, `locked`), nhật ký kiểm toán, che mặt nạ thông tin ngân hàng, chỉ số tổng quan hệ thống, probe sức khỏe.
- **_Tầng Worker Bất đồng bộ (Asynchronous Worker Layer):_**
  - **River Queue (Chạy trên PostgreSQL):** Các worker hàng đợi giao dịch xử lý `bill_ocr`, `bill_bulk_finalize_item`, `send_notification`, và `settlement_scan`.
  - **Các Bộ đếm Thời gian Nền (Tickers):** Dọn dẹp token/phiên hết hạn (24h) và thử lại việc xóa media Cloudinary mồ côi (60s).
- **_Tầng Dữ liệu & Hạ tầng (Data & Infrastructure Layer):_**
  - **PostgreSQL 18:** Cơ sở dữ liệu quan hệ chính sử dụng khóa chính UUID v7, connection pool `pgxpool`, và cơ chế pub-sub `LISTEN/NOTIFY`.
  - **Bộ Lắng nghe Thông báo Dùng chung:** Kết nối PostgreSQL chuyên dụng duy nhất trên mỗi instance lắng nghe các kênh `bill_events`, `group_events`, và `user_events`.
- **_Dịch vụ Bên ngoài (External Services):_**
  - **LlamaExtract / Gemini Flash:** Trích xuất Vision OCR hóa đơn.
  - **Cloudinary:** Lưu trữ đám mây cho ảnh hóa đơn, ảnh chụp bằng chứng chuyển khoản và ảnh đại diện người dùng.
  - **Firebase Cloud Messaging (FCM):** Chuyển phát thông báo đẩy chạy nền.
  - **Gmail SMTP:** Gửi email OTP giao dịch.
  - **VietQR / NAPAS 247:** Tiêu chuẩn QR liên ngân hàng và danh bạ ngân hàng.

---

### **6.2 Tech Stack** {#62-tech-stack}

| Danh mục | Công nghệ / Thư viện |
| :--- | :--- |
| **Ngôn ngữ Backend** | Go 1.24+ |
| **HTTP Router** | Chi v5 (`github.com/go-chi/chi/v5`) |
| **Cơ sở dữ liệu** | PostgreSQL 18, `pgx/v5` (`pgxpool`), Khóa chính UUID v7 |
| **SQL & Migrations** | `sqlc` (Sinh Go code SQL type-safe), `goose` (Quản lý migration tập trung) |
| **Hàng đợi Tác vụ** | River Queue (`github.com/riverqueue/river`) chạy trên PostgreSQL |
| **Thời gian thực (Realtime)** | Server-Sent Events (SSE) + PostgreSQL `LISTEN/NOTIFY` (Shared Listener) |
| **Framework Frontend** | Flutter 3.x (Dart 3.x) |
| **Quản lý Trạng thái** | Riverpod (StateNotifier / AsyncNotifier) |
| **DI & Mạng** | `get_it`, `injectable`, `dio`, `retrofit`, `freezed` |
| **Điều hướng (Routing)** | `go_router` (Guards, ShellRoute, Deep Linking) |
| **Lưu trữ Bảo mật** | `flutter_secure_storage` |
| **QR & Quét mã** | `mobile_scanner`, `zxing2`, VietQR TLV / Compact Image API |
| **OCR & Vision** | LlamaExtract / Gemini Flash API |
| **Lưu trữ & Media** | Cloudinary Go SDK & REST API |
| **Thông báo Đẩy** | Firebase Cloud Messaging (FCM) v1 API |
| **Gửi Email** | Gmail SMTP (TLS) |
| **Cổng Web Admin** | HTML5 / CSS3 / Vanilla JS tĩnh nhúng qua `//go:embed` |
| **Khả năng Quan sát** | Structured JSON logging (`slog`), Prometheus `/metrics`, `/health/ready` |
| **Container hóa** | Docker, Docker Compose |

##### **Bảng 4. Tech Stack** {#bảng-4-tech-stack}

---

## **7. Cột mốc & Lộ trình Phát triển** {#7-cột-mốc--lộ-trình-phát-triển}

| Giai đoạn | Sản phẩm bàn giao & Các Module đã Triển khai | Trạng thái |
| :---: | :--- | :---: |
| **M1** | **Kiến trúc, Cơ sở dữ liệu & Động cơ Xác thực Cốt lõi**<br>• Thiết lập Modular Monolith theo Clean Architecture & Ports/Adapters<br>• Migration PostgreSQL Goose & sinh truy vấn an toàn kiểu bằng sqlc<br>• Xác thực đơn phiên, băm mật khẩu bcrypt, JWT access token, xoay vòng refresh token, và xác thực email OTP 6 chữ số | ✅ **Đã hoàn thành** |
| **M2** | **Quản lý Nhóm & OCR Hóa đơn Bất đồng bộ**<br>• Vòng đời nhóm, mã mời Base62, sức chứa 50 thành viên, chuyển giao trưởng nhóm `NOWAIT`<br>• Thiết lập River Queue trên nền PostgreSQL<br>• Tải ảnh hóa đơn nhiều trang (HTTP 202 Accepted), tích hợp Cloudinary và worker OCR LlamaExtract | ✅ **Đã hoàn thành** |
| **M3** | **Động cơ Tính toán Chia tiền Hamilton & Quyết toán**<br>• Số học số hữu tỉ `big.Rat` và thuật toán chốt hóa đơn làm tròn số dư lớn nhất Hamilton<br>• Tạo VietQR động và intent thanh toán không khóa nợ `pending_proof`<br>• Nộp bằng chứng thanh toán 2 pha, tải ảnh Cloudinary và chủ nợ xác nhận/từ chối thủ công | ✅ **Đã hoàn thành** |
| **M4** | **Động cơ Realtime, Thông báo & Cổng Web Admin**<br>• Bộ lắng nghe PostgreSQL dùng chung duy nhất và luồng SSE người dùng hợp nhất (`GET /users/me/events`)<br>• Worker River `send_notification` và tích hợp thông báo đẩy Firebase FCM<br>• Cổng Web Admin nhúng (`/admin-portal/`), nhật ký kiểm toán và probe kiểm tra sức khỏe | ✅ **Đã hoàn thành** |
| **M5** | **Ứng dụng Di động Đa nền tảng & Tối ưu Toàn diện**<br>• Ứng dụng Flutter Clean Architecture (Trang chủ, Nhóm, Quét QR camera, Chụp hóa đơn, Quyết toán, Thông báo)<br>• Trình quản lý làm mới token `SessionRefresher`, vá danh sách tại chỗ, đếm ngược 24h nhắc nợ<br>• Kiểm thử tích hợp, kiểm toán bảo mật và tài liệu hóa toàn diện | ✅ **Đã hoàn thành** |

##### **Bảng 5. Cột mốc & Lộ trình** {#bảng-5-cột-mốc--lộ-trình}

---

## **8. Rủi ro & Giải pháp Giảm thiểu** {#8-rủi-ro--giải-pháp-giảm-thiểu}

| STT | Mức độ | Ưu tiên | Mô tả Rủi ro & Giải pháp Giảm thiểu Đã triển khai |
| :-: | :---: | :---: | :--- |
| R1 | CAO | TRUNG BÌNH | **Sai lệch OCR & Timeout của Nhà cung cấp:** Bố cục hóa đơn tiếng Việt phức tạp, tổng tiền viết tay hoặc rớt mạng có thể làm thất bại việc trích xuất. **Giải pháp:** Hàng đợi River bất đồng bộ với 3 lần thử lại; phản hồi HTTP 202 Accepted; bước kiểm tra cho phép nhập nháp thủ công và chỉnh sửa trực tiếp từng món trước khi chốt. |
| R2 | CAO | THẤP | **Sai lệch Bất biến Làm tròn:** Lỗi làm tròn số thực dấu phẩy động có thể vi phạm $\sum \text{debts} = \text{bill\_total}$. **Giải pháp:** Số học số nguyên `int64` VNĐ nghiêm ngặt và số hữu tỉ `big.Rat`; phân bổ số dư lớn nhất Hamilton với cơ chế phá vỡ thế hòa tất định theo UUID; độ bao phủ unit test 100%. |
| R3 | TRUNG BÌNH | TRUNG BÌNH | **Chậm trễ Xác nhận Thủ công:** Chủ nợ có thể quên hoặc chậm trễ xác nhận tiền đã nhận vào tài khoản ngân hàng. **Giải pháp:** Mã tham chiếu nổi bật (`PAY`+8 Base32); nhắc nợ thủ công (tối đa 3 lần, giãn cách $\ge 24$h); cảnh báo tự động hàng giờ từ job `settlement_scan` cho các bằng chứng chờ duyệt quá 48h. |
| R4 | CAO | CAO | **Chiếm đoạt Phiên & Tấn công Phát lại Token:** Refresh token bị lộ có thể kéo dài quyền truy cập trái phép. **Giải pháp:** Cơ chế xoay vòng Refresh Token kèm phát hiện tái sử dụng (thu hồi toàn bộ phiên ngay lập tức khi phát hiện phát lại); mỗi người dùng chỉ có 1 phiên hoạt động duy nhất; giới hạn tần suất chống brute-force (5 lần sai/15p $\implies$ khóa 15p). |
| R5 | CAO | TRUNG BÌNH | **Cạn kiệt Kết nối Realtime:** Mở nhiều kết nối SSE cho mỗi người dùng có thể làm cạn kiệt pool kết nối PostgreSQL. **Giải pháp:** Luồng SSE người dùng hợp nhất (`GET /users/me/events`); bộ lắng nghe PostgreSQL dùng chung duy nhất cho cả 3 kênh; thông báo vô hiệu hóa gọn nhẹ kèm re-fetch dữ liệu qua REST. |
| R6 | TRUNG BÌNH | THẤP | **Xung đột Chỉnh sửa Đồng thời:** Chỉnh sửa hóa đơn hoặc tham gia nhóm đồng thời có thể làm sai lệch số dư nhóm. **Giải pháp:** Khóa mức hàng trong cơ sở dữ liệu (`LockActiveGroup`), kiểm tra phiên bản lạc quan `version` CAS, và header `Idempotency-Key` tất định trên tất cả các thao tác quyết toán. |

##### **Bảng 6. Rủi ro & Giải pháp Giảm thiểu** {#bảng-6-rủi-ro--giải-pháp-giảm-thiểu}

---
