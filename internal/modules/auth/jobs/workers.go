package jobs

import (
	"context"
	"log"
	"time"

	"paysplit-backend/internal/modules/auth/domain"
)

type Repository interface {
	CleanupExpiredAuth(context.Context, time.Time, int) (int64, error)
	ClaimMediaCleanup(context.Context, time.Time, int) ([]domain.MediaCleanupJob, error)
	CompleteMediaCleanup(context.Context, string, time.Time) error
	FailMediaCleanup(context.Context, string, string, time.Time) error
}
type Storage interface {
	Delete(context.Context, string) error
}
type Workers struct {
	repo                                   Repository
	storage                                Storage
	authInterval, retention, mediaInterval time.Duration
}

func New(repo Repository, storage Storage, authInterval, retention, mediaInterval time.Duration) *Workers {
	if repo == nil || storage == nil || authInterval <= 0 || retention <= 0 || mediaInterval <= 0 {
		panic("cleanup worker dependencies must be valid")
	}
	return &Workers{repo: repo, storage: storage, authInterval: authInterval, retention: retention, mediaInterval: mediaInterval}
}
func (w *Workers) Run(ctx context.Context) {
	authTicker := time.NewTicker(w.authInterval)
	mediaTicker := time.NewTicker(w.mediaInterval)
	defer authTicker.Stop()
	defer mediaTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-authTicker.C:
			w.cleanupAuth(ctx, now)
		case now := <-mediaTicker.C:
			w.cleanupMedia(ctx, now)
		}
	}
}
func (w *Workers) cleanupAuth(ctx context.Context, now time.Time) {
	count, err := w.repo.CleanupExpiredAuth(ctx, now.Add(-w.retention), 500)
	if err != nil {
		log.Printf("event=auth_cleanup_failed err=%v", err)
		return
	}
	if count > 0 {
		log.Printf("event=auth_cleanup_completed rows=%d", count)
	}
}
func (w *Workers) cleanupMedia(ctx context.Context, now time.Time) {
	jobs, err := w.repo.ClaimMediaCleanup(ctx, now, 50)
	if err != nil {
		log.Printf("event=media_cleanup_claim_failed err=%v", err)
		return
	}
	for _, job := range jobs {
		if err = w.storage.Delete(ctx, job.ObjectKey); err == nil {
			if w.repo.CompleteMediaCleanup(ctx, job.ID, time.Now()) != nil {
				log.Printf("event=media_cleanup_complete_write_failed job_id=%s", job.ID)
			}
			continue
		}
		backoff := time.Minute * time.Duration(1<<min(job.AttemptCount-1, 10))
		if backoff > 24*time.Hour {
			backoff = 24 * time.Hour
		}
		if w.repo.FailMediaCleanup(ctx, job.ID, "delete_failed", time.Now().Add(backoff)) != nil {
			log.Printf("event=media_cleanup_retry_write_failed job_id=%s", job.ID)
		} else {
			log.Printf("event=media_cleanup_retry_scheduled job_id=%s attempt=%d", job.ID, job.AttemptCount)
		}
	}
}
