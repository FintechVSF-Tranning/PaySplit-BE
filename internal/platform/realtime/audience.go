package realtime

import (
	"bytes"
	"sort"

	"github.com/google/uuid"
)

const MaxAudience = 50

// NormalizeAudience chuẩn hóa danh sách người nhận của một sự kiện dữ liệu và
// cắt ở [MaxAudience]. Cắt bớt ở đây chỉ làm mất một thông báo làm mới.
//
// Không dùng cho `target_sids` của các control phiên: ở đó mất một SID nghĩa là
// một phiên đã bị thu hồi vẫn tiếp tục nhận sự kiện. Dùng [NormalizeSIDs].
func NormalizeAudience(ids []uuid.UUID) []uuid.UUID {
	out := NormalizeSIDs(ids)
	if len(out) > MaxAudience {
		out = out[:MaxAudience]
	}
	return out
}

// NormalizeSIDs sắp xếp và khử trùng lặp mà không cắt bớt. Danh sách control
// phải giữ nguyên vẹn; phần chia lô để vừa payload NOTIFY do người phát lo.
func NormalizeSIDs(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	filtered := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	sort.Slice(filtered, func(i, j int) bool {
		return bytes.Compare(filtered[i][:], filtered[j][:]) < 0
	})
	out := filtered[:0]
	var prev uuid.UUID
	for i, id := range filtered {
		if i == 0 || id != prev {
			out = append(out, id)
			prev = id
		}
	}
	return out
}

func AudienceStrings(ids []uuid.UUID) []string {
	normalized := NormalizeAudience(ids)
	out := make([]string, 0, len(normalized))
	for _, id := range normalized {
		out = append(out, id.String())
	}
	return out
}
