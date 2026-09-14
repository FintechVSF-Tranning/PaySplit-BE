package vietqr

import (
	"strings"
	"testing"
)

func TestGeneratorBuildGolden(t *testing.T) {
	g := New("https://img.vietqr.io/image", "compact", "")
	payload, imageURL, err := g.Build("970436", "0123456789", "NGUYEN VAN A", "PAYABCDEFGH", 125000)
	if err != nil {
		t.Fatal(err)
	}
	const want = "00020101021238540010A00000072701240006970436011001234567890208QRIBFTTA530370454061250005802VN62150811PAYABCDEFGH63046729"
	if payload != want {
		t.Fatalf("payload mismatch\nwant %s\n got %s", want, payload)
	}
	if !strings.Contains(imageURL, "970436-0123456789-compact.png") || !strings.Contains(imageURL, "accountName=NGUYEN%20VAN%20A") || !strings.Contains(imageURL, "amount=125000") {
		t.Fatalf("unexpected URL %s", imageURL)
	}
}

func TestGeneratorRejectsInvalidInput(t *testing.T) {
	if _, _, err := New("", "", "").Build("", "1", "A", "PAYABCDEFGH", 1); err == nil {
		t.Fatal("expected invalid input error")
	}
}

func TestGeneratorRejectsTLVValuesOver99Bytes(t *testing.T) {
	if _, _, err := New("", "", "").Build("970436", strings.Repeat("1", 100), "A", "PAYABCDEFGH", 1); err == nil {
		t.Fatal("expected oversized TLV error")
	}
}

// VietinBank cá nhân chỉ đẩy giao dịch sang SePay khi nội dung chuyển khoản bắt
// đầu bằng từ khóa của họ, nên tiền tố phải nằm trong cả payload lẫn ảnh QR.
func TestGeneratorPutsContentPrefixBeforeReference(t *testing.T) {
	g := New("https://img.vietqr.io/image", "compact", "SEVQR")
	if got := g.Content("PAYABCDEFGH"); got != "SEVQR PAYABCDEFGH" {
		t.Fatalf("Content() = %q", got)
	}
	payload, imageURL, err := g.Build("970436", "0123456789", "NGUYEN VAN A", "PAYABCDEFGH", 125000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, "0817SEVQR PAYABCDEFGH") {
		t.Fatalf("payload missing prefixed content: %s", payload)
	}
	if !strings.Contains(imageURL, "addInfo=SEVQR%20PAYABCDEFGH") {
		t.Fatalf("image URL missing prefixed content: %s", imageURL)
	}
	if empty := New("", "", "").Content("PAYABCDEFGH"); empty != "PAYABCDEFGH" {
		t.Fatalf("no prefix configured should keep the reference as is, got %q", empty)
	}
}
