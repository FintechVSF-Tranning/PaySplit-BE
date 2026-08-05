# 💸 PaySplit – Smart Bill Splitter & Settlement

PaySplit is a mobile-first solution that takes the awkwardness and math out of group expenses. By combining AI OCR with Dynamic VietQR, it automates receipt parsing, bill splitting, and debt reminders.

## 🚀 Core Features (MVP)
* **Smart OCR:** Instantly scan receipts to extract items, prices, and auto-calculate VAT/service fees.
* **Dynamic VietQR:** Generate user-specific QR codes for precise payment amounts.
* **Smart Reminders:** Automated, friendly notifications sent via chat platforms to debtors.
* **1-Click Settlement:** Tap to pay instantly without manual data entry.

## 🛠 Tech Stack
* **Mobile Client:** Flutter (Dart) - Cross-platform UI.
* **Backend API:** Go (Golang) with Gin/Fiber - High-performance REST & WebSocket server.
* **Database:** PostgreSQL.
* **Real-time:** WebSockets (Gorilla WebSocket) for instant payment status updates.
* **AI/Integration:** OCR processing & VietQR API.
