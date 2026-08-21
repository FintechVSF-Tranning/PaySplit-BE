package cloudinary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	cld "github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"

	"paysplit-backend/internal/config"
)

type AvatarStorage struct {
	client  *cld.Cloudinary
	timeout time.Duration
}

func New(cfg config.CloudinaryConfig, timeout time.Duration) (*AvatarStorage, error) {
	client, err := cld.NewFromParams(cfg.CloudName, cfg.APIKey, cfg.APISecret)
	if err != nil {
		return nil, fmt.Errorf("create Cloudinary client: %w", err)
	}
	return &AvatarStorage{client: client, timeout: timeout}, nil
}
func (s *AvatarStorage) Upload(ctx context.Context, data []byte, publicID string) (string, error) {
	uploadCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	overwrite := false
	unique := false
	result, err := s.client.Upload.Upload(uploadCtx, bytes.NewReader(data), uploader.UploadParams{PublicID: publicID, Format: "webp", ResourceType: "image", Overwrite: &overwrite, UniqueFilename: &unique})
	if err != nil {
		return "", fmt.Errorf("upload Cloudinary avatar: %w", err)
	}
	if result == nil || result.PublicID == "" || !strings.EqualFold(result.Format, "webp") {
		return "", errors.New("Cloudinary did not return a WebP asset")
	}
	return result.PublicID, nil
}
func (s *AvatarStorage) Delete(ctx context.Context, publicID string) error {
	deleteCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	invalidate := true
	result, err := s.client.Upload.Destroy(deleteCtx, uploader.DestroyParams{PublicID: publicID, ResourceType: "image", Invalidate: &invalidate})
	if err != nil {
		return fmt.Errorf("destroy Cloudinary avatar: %w", err)
	}
	if result == nil {
		return errors.New("empty Cloudinary destroy response")
	}
	if result.Result != "ok" {
		return fmt.Errorf("destroy Cloudinary avatar: result=%q", result.Result)
	}
	return nil
}
func (s *AvatarStorage) URL(publicID string) string {
	asset, err := s.client.Image(publicID)
	if err != nil {
		return ""
	}
	value, err := asset.String()
	if err != nil {
		return ""
	}
	return value
}
