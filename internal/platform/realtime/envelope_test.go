package realtime

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNormalizeAudienceSortsAndDeduplicates(t *testing.T) {
	a := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	b := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	got := NormalizeAudience([]uuid.UUID{a, uuid.Nil, b, a})
	if len(got) != 2 || got[0] != b || got[1] != a {
		t.Fatalf("NormalizeAudience() = %v", got)
	}
}

func TestDecodeUserEnvelopeRejectsUnknownKindAndConflict(t *testing.T) {
	if _, err := DecodeUserEnvelope(`{"schema_version":2,"kind":"invalidate"}`); err != ErrUnknownSchema {
		t.Fatalf("unknown schema err = %v", err)
	}
	if _, err := DecodeUserEnvelope(`{"schema_version":1,"kind":"nope"}`); err != ErrUnknownKind {
		t.Fatalf("unknown kind err = %v", err)
	}
	payload := `{"schema_version":1,"kind":"invalidate","audience_user_ids":["00000000-0000-0000-0000-000000000001"],"target_sids":["00000000-0000-0000-0000-000000000002"],"body":{"scope":"group","group_id":"00000000-0000-0000-0000-000000000003","type":"x"}}`
	if _, err := DecodeUserEnvelope(payload); err != ErrConflictingRecipient {
		t.Fatalf("conflict err = %v", err)
	}
}

func TestEncodeInvalidateRejectsOversizedAndEncodesStreamReplace(t *testing.T) {
	sid := uuid.Must(uuid.NewV7())
	streamID := uuid.Must(uuid.NewV7())
	payload, err := EncodeStreamReplace(sid, streamID)
	if err != nil {
		t.Fatal(err)
	}
	env, err := DecodeUserEnvelope(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != KindStreamReplace || env.ReplacementStreamID == nil || *env.ReplacementStreamID != streamID {
		t.Fatalf("replace envelope = %+v", env)
	}

	gid := uuid.Must(uuid.NewV7())
	audience := []uuid.UUID{uuid.Must(uuid.NewV7())}
	body := InvalidateBody{Scope: ScopeGroup, GroupID: gid, Type: "group.bill_submission_locked"}
	encoded, err := EncodeInvalidate(audience, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeUserEnvelope(string(encoded)); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidateRoundTripsNotificationScope(t *testing.T) {
	gid := uuid.Must(uuid.NewV7())
	// Audience của scope này là danh sách người nhận thông báo, không phải cả nhóm.
	audience := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	body := InvalidateBody{Scope: ScopeNotification, GroupID: gid, Type: TypeNotificationCreated}

	encoded, err := EncodeInvalidate(audience, body)
	if err != nil {
		t.Fatalf("EncodeInvalidate() error = %v", err)
	}
	env, err := DecodeUserEnvelope(string(encoded))
	if err != nil {
		t.Fatalf("DecodeUserEnvelope() error = %v", err)
	}
	if env.Body == nil || env.Body.Scope != ScopeNotification || env.Body.Type != TypeNotificationCreated {
		t.Fatalf("decoded body = %+v", env.Body)
	}
	if len(env.AudienceUserIDs) != 2 {
		t.Fatalf("audience = %v, want 2 người nhận", env.AudienceUserIDs)
	}

	// GroupID vẫn bắt buộc: thông báo nào cũng gắn với một nhóm.
	if _, err = EncodeInvalidate(audience, InvalidateBody{Scope: ScopeNotification, Type: TypeNotificationCreated}); err != ErrInvalidUUID {
		t.Fatalf("thiếu group_id err = %v, want %v", err, ErrInvalidUUID)
	}
}

func TestEncodeSessionEndedOmitsControlFields(t *testing.T) {
	// covers: AC-14, AC-15
	sid := uuid.Must(uuid.NewV7())
	payload, err := EncodeSessionEnded([]uuid.UUID{sid, uuid.Nil, sid})
	if err != nil {
		t.Fatal(err)
	}
	env, err := DecodeUserEnvelope(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != KindSessionEnded || len(env.TargetSIDs) != 1 || env.TargetSIDs[0] != sid {
		t.Fatalf("session ended envelope = %+v", env)
	}
	if env.Body != nil || env.ReplacementStreamID != nil || len(env.AudienceUserIDs) != 0 {
		t.Fatalf("session ended leaked control fields: %+v", env)
	}
}

func TestEncodeGroupEnvelopeDropsDataWhenOversized(t *testing.T) {
	// covers: AC-15, AC-19
	gid := uuid.Must(uuid.NewV7())
	audience := []uuid.UUID{uuid.Must(uuid.NewV7())}
	big := make([]byte, 0, MaxNotifyPayload+32)
	big = append(big, '"')
	for i := 0; i < MaxNotifyPayload; i++ {
		big = append(big, 'a')
	}
	big = append(big, '"')
	payload, thin, err := EncodeGroupEnvelope(GroupEnvelope{
		GroupID:         gid,
		Version:         2,
		Type:            "member_joined",
		Data:            big,
		AudienceUserIDs: audience,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !thin {
		t.Fatal("expected thin envelope when public data cannot fit")
	}
	env, err := DecodeGroupEnvelope(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 0 {
		t.Fatalf("thin envelope still carried data of %d bytes", len(env.Data))
	}
	if env.GroupID != gid || env.Version != 2 || len(env.AudienceUserIDs) != 1 {
		t.Fatalf("thin envelope dropped routing fields: %+v", env)
	}
}

func TestPublisherDisabledSkipsUserEvents(t *testing.T) {
	// covers: AC-23
	var p *Publisher
	if err := p.NotifyInvalidate(nil, nil, []uuid.UUID{uuid.Must(uuid.NewV7())}, InvalidateBody{}); err != nil {
		t.Fatalf("nil publisher = %v", err)
	}
	disabled := &Publisher{Enabled: false}
	if err := disabled.NotifySessionEnded(nil, nil, []uuid.UUID{uuid.Must(uuid.NewV7())}); err != nil {
		t.Fatalf("disabled publisher = %v", err)
	}
}

func TestParseAppVersion(t *testing.T) {
	got := ParseAppVersion("1.4.0+27")
	if got.Unknown || got.Major != 1 || got.Minor != 4 || got.Patch != 0 || got.Build != 27 {
		t.Fatalf("ParseAppVersion = %+v", got)
	}
	if ParseAppVersion("bad").Class(got) != "unknown" {
		t.Fatal("invalid version should be unknown")
	}
	if ParseAppVersion("1.4.0+26").Class(got) != "legacy" {
		t.Fatal("older build should be legacy")
	}
	if ParseAppVersion("1.4.0+27").Class(got) != "supported" {
		t.Fatal("matching version should be supported")
	}
}

// Control thu hồi phiên không được cắt bớt: SID sống thường là UUIDv7 mới nhất,
// nằm cuối danh sách đã sắp xếp, nên một giới hạn 50 sẽ bỏ đúng phiên cần đóng.
func TestSessionEndedControlKeepsEverySIDBeyondAudienceCap(t *testing.T) {
	// covers: AC-14
	const total = MaxAudience + 25
	sids := make([]uuid.UUID, 0, total)
	for i := 0; i < total; i++ {
		sids = append(sids, uuid.Must(uuid.NewV7()))
	}
	live := sids[len(sids)-1]

	payload, err := EncodeSessionEnded(sids)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	env, err := DecodeUserEnvelope(string(payload))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.TargetSIDs) != total {
		t.Fatalf("target_sids = %d, want %d", len(env.TargetSIDs), total)
	}
	var sawLive bool
	for _, sid := range env.TargetSIDs {
		if sid == live {
			sawLive = true
		}
	}
	if !sawLive {
		t.Fatal("the newest live SID was dropped from the control")
	}
}

func TestNotifySessionEndedChunksInsteadOfDropping(t *testing.T) {
	// covers: AC-14
	const total = maxSessionEndedSIDs*2 + 7
	sids := make([]uuid.UUID, 0, total)
	for i := 0; i < total; i++ {
		sids = append(sids, uuid.Must(uuid.NewV7()))
	}
	exec := &capturingExec{}

	if err := (&Publisher{Enabled: true}).NotifySessionEnded(context.Background(), exec, sids); err != nil {
		t.Fatalf("notify: %v", err)
	}

	seen := make(map[uuid.UUID]bool, total)
	for _, payload := range exec.payloads {
		if len(payload) > MaxNotifyPayload {
			t.Fatalf("chunk of %d bytes exceeds the NOTIFY limit", len(payload))
		}
		env, err := DecodeUserEnvelope(payload)
		if err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		for _, sid := range env.TargetSIDs {
			seen[sid] = true
		}
	}
	if len(seen) != total {
		t.Fatalf("delivered %d distinct SIDs, want %d", len(seen), total)
	}
}

type capturingExec struct{ payloads []string }

func (e *capturingExec) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if len(args) == 2 {
		if payload, ok := args[1].(string); ok {
			e.payloads = append(e.payloads, payload)
		}
	}
	return pgconn.NewCommandTag("SELECT"), nil
}
