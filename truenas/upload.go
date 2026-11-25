package truenas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"time"
)

// UploadFile submits a TrueNAS file-upload API call without placing file
// contents in WebSocket request logs or error messages. TrueNAS expects the
// method payload in the multipart "data" field followed by the file part.
func (c *Client) UploadFile(method string, args []interface{}, filename string, contents []byte) (int, error) {
	const maxUploadBytes = 16 * 1024 * 1024
	if len(contents) > maxUploadBytes {
		return 0, fmt.Errorf("file exceeds the %d-byte safety limit", maxUploadBytes)
	}

	baseURL, err := c.httpBaseURL()
	if err != nil {
		return 0, err
	}
	baseURL.Path = "/_upload/"

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadata, err := json.Marshal(map[string]interface{}{"method": method, "params": args})
	if err != nil {
		return 0, fmt.Errorf("failed to encode upload metadata: %w", err)
	}
	if err := writer.WriteField("data", string(metadata)); err != nil {
		return 0, fmt.Errorf("failed to write upload metadata: %w", err)
	}
	part, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return 0, fmt.Errorf("failed to create upload part: %w", err)
	}
	if _, err := part.Write(contents); err != nil {
		return 0, fmt.Errorf("failed to write upload contents: %w", err)
	}
	if err := writer.Close(); err != nil {
		return 0, fmt.Errorf("failed to finalize upload: %w", err)
	}

	request, err := http.NewRequest(http.MethodPost, baseURL.String(), &body)
	if err != nil {
		return 0, fmt.Errorf("failed to create upload request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", writer.FormDataContentType())

	httpClient := &http.Client{
		Timeout:   60 * time.Second,
		Transport: &http.Transport{TLSClientConfig: c.tlsConfig},
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("failed to upload file: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("file upload returned HTTP status %s", response.Status)
	}

	var uploadResponse struct {
		JobID int `json:"job_id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&uploadResponse); err != nil {
		return 0, fmt.Errorf("failed to parse file upload response: %w", err)
	}
	if uploadResponse.JobID <= 0 {
		return 0, fmt.Errorf("file upload response did not contain a valid job_id")
	}
	return uploadResponse.JobID, nil
}
