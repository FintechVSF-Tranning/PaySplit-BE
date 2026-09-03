package llamaextract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"paysplit-backend/internal/config"
	"paysplit-backend/internal/modules/bill/domain"
	"paysplit-backend/internal/modules/bill/usecase"
)

var _ usecase.OCRProvider = (*Client)(nil)

// maxProviderMessage giới hạn phần văn bản của provider được đưa vào error.
const maxProviderMessage = 120

// truncateProviderMessage cắt ngắn thông điệp lỗi của provider.
//
// Lỗi của adapter này đi tiếp vào log của worker, nên không được mang theo thân
// phản hồi: nó có thể chứa chính nội dung hóa đơn đã bóc tách, và log không nằm
// trong đường lưu trữ raw OCR có kiểm soát.
func truncateProviderMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) <= maxProviderMessage {
		return msg
	}
	return msg[:maxProviderMessage] + "…"
}

// Client là HTTP client adapter kết nối tới LlamaExtract / LlamaCloud (Spec 3 AC-3).
type Client struct {
	apiKey     string
	endpoint   string
	timeout    time.Duration
	httpClient *http.Client
}

// New khởi tạo LlamaExtract Client từ cấu hình OCRConfig.
func New(cfg config.OCRConfig) *Client {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = "https://api.cloud.llamaindex.ai"
	}
	timeout := cfg.ProviderTimeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}

	return &Client{
		apiKey:   strings.TrimSpace(cfg.APIKey),
		endpoint: strings.TrimRight(endpoint, "/"),
		timeout:  timeout,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// SetHTTPClient hỗ trợ inject mock HTTP client cho unit tests.
func (c *Client) SetHTTPClient(client *http.Client) {
	c.httpClient = client
}

// ExtractReceipt thực hiện quy trình 3 bước gửi ảnh sang LlamaExtract, polling kết quả
// và chuẩn hóa thành domain.OCRCandidate (Spec 3 AC-3).
func (c *Client) ExtractReceipt(ctx context.Context, imageBytes []byte, mimeType string) (*domain.OCRCandidate, []byte, error) {
	if c.apiKey == "" {
		return nil, nil, domain.ErrOcrProviderUnavailable
	}
	if len(imageBytes) == 0 {
		return nil, nil, domain.ErrInvalidInput
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	effectiveTimeout := c.timeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < effectiveTimeout {
			effectiveTimeout = remaining
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, effectiveTimeout)
	defer cancel()

	// Bước 1: Upload file ảnh tạm thời lên LlamaCloud
	fileID, err := c.uploadFile(reqCtx, imageBytes, mimeType)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return nil, nil, domain.ErrOcrTimeout
		}
		return nil, nil, fmt.Errorf("%w: upload receipt image: %v", domain.ErrOcrProviderUnavailable, err)
	}

	// Bước 2: Kích hoạt tác vụ bóc tách (Extraction Job)
	jobID, err := c.createExtractionJob(reqCtx, fileID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return nil, nil, domain.ErrOcrTimeout
		}
		return nil, nil, fmt.Errorf("%w: trigger extraction job: %v", domain.ErrOcrProviderUnavailable, err)
	}

	// Bước 3: Polling trạng thái cho đến khi job COMPLETED
	rawResultBytes, err := c.pollExtractionResult(reqCtx, jobID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return nil, nil, domain.ErrOcrTimeout
		}
		return nil, nil, err
	}

	// Bước 4: Chuẩn hóa kết quả sang domain.OCRCandidate
	candidate, err := Normalize(rawResultBytes)
	if err != nil {
		return nil, rawResultBytes, err
	}

	return candidate, rawResultBytes, nil
}

// uploadFile upload multipart file ảnh lên LlamaCloud API.
func (c *Client) uploadFile(ctx context.Context, imageBytes []byte, mimeType string) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	filename := "receipt.jpg"
	if strings.Contains(mimeType, "png") {
		filename = "receipt.png"
	}

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(imageBytes); err != nil {
		return "", err
	}

	if err := writer.WriteField("purpose", "extract"); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	uploadURL := fmt.Sprintf("%s/api/v1/beta/files", c.endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upload failed with status %d", resp.StatusCode)
	}

	var uploadResp struct {
		ID     string `json:"id"`
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(respBytes, &uploadResp); err != nil {
		return "", err
	}

	fileID := uploadResp.ID
	if fileID == "" {
		fileID = uploadResp.FileID
	}
	if fileID == "" {
		return "", errors.New("empty file id in upload response")
	}

	return fileID, nil
}

// createExtractionJob gửi schema yêu cầu bóc tách tới endpoint /api/v2/extract.
func (c *Client) createExtractionJob(ctx context.Context, fileID string) (string, error) {
	payload := map[string]any{
		"file_input": fileID,
		"configuration": map[string]any{
			"data_schema":       ReceiptSchema(),
			"confidence_scores": true,
		},
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	extractURL := fmt.Sprintf("%s/api/v2/extract", c.endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, extractURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("create extract job failed with status %d", resp.StatusCode)
	}

	var jobResp struct {
		ID    string `json:"id"`
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(respBytes, &jobResp); err != nil {
		return "", err
	}

	jobID := jobResp.ID
	if jobID == "" {
		jobID = jobResp.JobID
	}
	if jobID == "" {
		return "", errors.New("empty job id in extract job response")
	}

	return jobID, nil
}

// pollExtractionResult lặp kiểm tra trạng thái job cho đến khi COMPLETED.
func (c *Client) pollExtractionResult(ctx context.Context, jobID string) ([]byte, error) {
	pollURL := fmt.Sprintf("%s/api/v2/extract/%s?expand=extract_metadata", c.endpoint, jobID)
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+c.apiKey)

			resp, err := c.httpClient.Do(req)
			if err != nil {
				return nil, err
			}

			respBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, err
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return nil, fmt.Errorf("poll failed with status %d", resp.StatusCode)
			}

			var statusResp struct {
				Status        string          `json:"status"`
				State         string          `json:"state"`
				ExtractResult json.RawMessage `json:"extract_result"`
				Data          json.RawMessage `json:"data"`
				Error         string          `json:"error"`
			}
			if err := json.Unmarshal(respBytes, &statusResp); err != nil {
				return nil, fmt.Errorf("%w: parse poll response", domain.ErrOcrSchemaInvalid)
			}

			status := strings.ToUpper(statusResp.Status)
			if status == "" {
				status = strings.ToUpper(statusResp.State)
			}

			switch status {
			case "COMPLETED", "SUCCESS":
				if len(statusResp.ExtractResult) > 0 {
					return statusResp.ExtractResult, nil
				}
				if len(statusResp.Data) > 0 {
					return statusResp.Data, nil
				}
				return respBytes, nil
			case "FAILED", "ERROR":
				errMsg := statusResp.Error
				if errMsg == "" {
					errMsg = "unknown extraction error"
				}
				return nil, fmt.Errorf("%w: %s", domain.ErrOcrProviderUnavailable, truncateProviderMessage(errMsg))
			case "PENDING", "PROCESSING", "RUNNING", "QUEUED":
				// Tiếp tục chờ lần ticker tiếp theo
				continue
			default:
				// Tiếp tục chờ
				continue
			}
		}
	}
}
