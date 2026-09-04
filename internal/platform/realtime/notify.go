package realtime

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type Executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type Publisher struct {
	Enabled bool
}

func Notify(ctx context.Context, exec Executor, channel string, payload []byte) error {
	if exec == nil {
		return fmt.Errorf("realtime notify executor must not be nil")
	}
	if _, err := exec.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, string(payload)); err != nil {
		return fmt.Errorf("pg_notify %s: %w", channel, err)
	}
	return nil
}

func NotifyGroup(ctx context.Context, exec Executor, env GroupEnvelope) error {
	payload, _, err := EncodeGroupEnvelope(env)
	if err != nil {
		return err
	}
	return Notify(ctx, exec, ChannelGroupEvents, payload)
}

func NotifyBill(ctx context.Context, exec Executor, env BillEnvelope) error {
	payload, err := EncodeBillEnvelope(env)
	if err != nil {
		return err
	}
	return Notify(ctx, exec, ChannelBillEvents, payload)
}

func (p *Publisher) NotifyInvalidate(ctx context.Context, exec Executor, audience []uuid.UUID, body InvalidateBody) error {
	if p == nil || !p.Enabled {
		return nil
	}
	payload, err := EncodeInvalidate(audience, body)
	if err != nil {
		return err
	}
	return Notify(ctx, exec, ChannelUserEvents, payload)
}

// maxSessionEndedSIDs là số SID tối đa trong một control `session.ended`. Với
// giới hạn [MaxNotifyPayload] 7000 byte, mỗi UUID chiếm 39 byte trong JSON nên
// 100 SID còn dư chỗ an toàn.
const maxSessionEndedSIDs = 100

// NotifySessionEnded phát control thu hồi phiên. Danh sách dài được chia thành
// nhiều NOTIFY trong cùng transaction của caller, nên không SID nào bị bỏ rơi mà
// vẫn giữ nguyên ngữ nghĩa "tất cả hoặc không" khi commit.
func (p *Publisher) NotifySessionEnded(ctx context.Context, exec Executor, sids []uuid.UUID) error {
	if p == nil || !p.Enabled {
		return nil
	}
	sids = NormalizeSIDs(sids)
	if len(sids) == 0 {
		return nil
	}
	for start := 0; start < len(sids); start += maxSessionEndedSIDs {
		end := start + maxSessionEndedSIDs
		if end > len(sids) {
			end = len(sids)
		}
		payload, err := EncodeSessionEnded(sids[start:end])
		if err != nil {
			return err
		}
		if err = Notify(ctx, exec, ChannelUserEvents, payload); err != nil {
			return err
		}
	}
	return nil
}

func (p *Publisher) NotifyStreamReplace(ctx context.Context, exec Executor, sid, replacementStreamID uuid.UUID) error {
	if p == nil || !p.Enabled {
		return fmt.Errorf("user stream is disabled")
	}
	payload, err := EncodeStreamReplace(sid, replacementStreamID)
	if err != nil {
		return err
	}
	return Notify(ctx, exec, ChannelUserEvents, payload)
}
