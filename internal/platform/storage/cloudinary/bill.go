package cloudinary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	cld "github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api"
	"github.com/cloudinary/cloudinary-go/v2/api/admin"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"

	"paysplit-backend/internal/config"
	"paysplit-backend/internal/modules/bill/usecase"
)

var _ usecase.BillStorage = (*BillStorage)(nil)

// BillStorage quản lý việc lưu trữ ảnh hóa đơn riêng tư, ký URL tạm thời và dọn dẹp trên Cloudinary (Spec 3 AC-1, AC-8, AC-13).
type BillStorage struct {
	client     *cld.Cloudinary
	timeout    time.Duration
	httpClient *http.Client
}

// NewBillStorage khởi tạo Cloudinary Bill Storage adapter.
func NewBillStorage(cfg config.CloudinaryConfig, timeout time.Duration) (*BillStorage, error) {
	client, err := cld.NewFromParams(cfg.CloudName, cfg.APIKey, cfg.APISecret)
	if err != nil {
		return nil, fmt.Errorf("create Cloudinary bill storage client: %w", err)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &BillStorage{
		client:     client,
		timeout:    timeout,
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

// Upload lưu trữ ảnh hóa đơn lên Cloudinary dưới dạng Private Asset (Spec 3 AC-1).
// Public ID tuân thủ quy ước: "bills/{operation_id}/{position}".
func (s *BillStorage) Upload(ctx context.Context, data []byte, publicID string) (string, error) {
	uploadCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	overwrite := true
	unique := false

	result, err := s.client.Upload.Upload(uploadCtx, bytes.NewReader(data), uploader.UploadParams{
		PublicID:       publicID,
		ResourceType:   "image",
		Type:           api.Private,
		Format:         "jpg",
		Overwrite:      &overwrite,
		UniqueFilename: &unique,
	})
	if err != nil {
		return "", fmt.Errorf("upload Cloudinary bill receipt: %w", err)
	}
	if result != nil && result.Error.Message != "" {
		return "", fmt.Errorf("upload Cloudinary bill receipt: %s", result.Error.Message)
	}
	if result == nil || result.PublicID == "" {
		return "", errors.New("empty Cloudinary upload response")
	}

	return result.PublicID, nil
}

// SignedURL sinh URL truy cập tạm thời có chữ ký bảo mật cho ảnh hóa đơn private (Spec 3 AC-8, AC-12).
func (s *BillStorage) SignedURL(publicID string, ttl time.Duration) (string, error) {
	if publicID == "" {
		return "", errors.New("publicID must not be empty")
	}

	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	query := url.Values{
		"expires_at": {strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)},
		"format":     {"jpg"},
		"public_id":  {publicID},
		"type":       {string(api.Private)},
	}
	signature, err := api.SignParametersUsingAlgoAndVersion(
		query,
		s.client.Config.Cloud.APISecret,
		s.client.Config.Cloud.GetSignatureAlgorithm(),
		s.client.Config.Cloud.GetSignatureVersion(),
	)
	if err != nil {
		return "", fmt.Errorf("generate signed url: %w", err)
	}
	query.Set("api_key", s.client.Config.Cloud.APIKey)
	query.Set("signature", signature)
	urlStr := fmt.Sprintf(
		"%s/v1_1/%s/image/download?%s",
		strings.TrimRight(s.client.Config.API.UploadPrefix, "/"),
		s.client.Config.Cloud.CloudName,
		query.Encode(),
	)

	return urlStr, nil
}

// Download tải byte ảnh riêng tư từ Cloudinary về cho River Background Worker xử lý OCR.
func (s *BillStorage) Download(ctx context.Context, publicID string) ([]byte, error) {
	signedURL, err := s.SignedURL(publicID, s.timeout)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download private receipt from Cloudinary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// Delete xóa một ảnh hóa đơn cụ thể trên Cloudinary.
func (s *BillStorage) Delete(ctx context.Context, publicID string) error {
	deleteCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	invalidate := true
	result, err := s.client.Upload.Destroy(deleteCtx, uploader.DestroyParams{
		PublicID:     publicID,
		ResourceType: "image",
		Type:         api.Private,
		Invalidate:   &invalidate,
	})
	if err != nil {
		return fmt.Errorf("destroy Cloudinary bill receipt: %w", err)
	}
	if result != nil && result.Error.Message != "" {
		return fmt.Errorf("destroy Cloudinary bill receipt: %s", result.Error.Message)
	}
	if result == nil {
		return errors.New("empty Cloudinary destroy response")
	}
	if result.Result != "ok" {
		return fmt.Errorf("destroy Cloudinary bill receipt: result=%q", result.Result)
	}

	return nil
}

// DeleteByPrefix xóa toàn bộ ảnh hóa đơn theo tiền tố prefix, ví dụ "bills/{operation_id}/" (Spec 3 AC-13).
func (s *BillStorage) DeleteByPrefix(ctx context.Context, prefix string) error {
	deleteCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	_, err := s.client.Admin.DeleteAssetsByPrefix(deleteCtx, admin.DeleteAssetsByPrefixParams{
		Prefix:       api.CldAPIArray{prefix},
		AssetType:    api.Image,
		DeliveryType: api.Private,
	})
	if err != nil {
		return fmt.Errorf("delete Cloudinary assets by prefix: %w", err)
	}

	return nil
}
