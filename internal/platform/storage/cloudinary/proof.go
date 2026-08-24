package cloudinary

import (
	"context"
	"time"

	"paysplit-backend/internal/config"
	settlementusecase "paysplit-backend/internal/modules/settlement/usecase"
)

var _ settlementusecase.ProofStorage = (*ProofStorage)(nil)

// ProofStorage stores private payment proofs as WebP while accepting any image
// format validated by the settlement use case.
type ProofStorage struct {
	base *BillStorage
}

func NewProofStorage(cfg config.CloudinaryConfig, timeout time.Duration) (*ProofStorage, error) {
	base, err := NewBillStorage(cfg, timeout)
	if err != nil {
		return nil, err
	}
	return &ProofStorage{base: base}, nil
}

func (s *ProofStorage) Upload(ctx context.Context, data []byte, publicID string) (string, error) {
	return s.base.upload(ctx, data, publicID, "webp", "q_100", "payment proof")
}

func (s *ProofStorage) SignedURL(publicID string, ttl time.Duration) (string, error) {
	return s.base.signedURL(publicID, ttl, "webp")
}

func (s *ProofStorage) Delete(ctx context.Context, publicID string) error {
	return s.base.Delete(ctx, publicID)
}
