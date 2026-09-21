// pi: src/core/bug-report-upload.ts — upload a report as multipart form data,
// anonymously or attributed to a Radius account (pi v0.86.0). pi's
// options.signal is dropped — cancellation travels as the Go ctx
// (AbortSignal → context rule).
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
)

// UploadBugReportOptions are the uploadBugReport parameters (pi:
// UploadBugReportOptions).
type UploadBugReportOptions struct {
	Token      string
	GatewayURL string
}

// UploadBugReport uploads the bundle to the Radius gateway (pi:
// uploadBugReport). Returns the report id.
func UploadBugReport(ctx context.Context, bundle *BugReportBundle, options UploadBugReportOptions) (string, error) {
	gateway := options.GatewayURL
	if gateway == "" {
		gateway = GetRadiusGatewayUrl()
	}
	endpoint, err := url.Parse(gateway)
	if err != nil {
		return "", fmt.Errorf("Bug report upload failed: %v", err)
	}
	endpoint.Path = endpoint.Path + "/v1/bug-reports"

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range bugReportFiles(bundle) {
		// FormData.append(name, blob, filename) → a form-data part carrying
		// both the field name and the file name.
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q; filename=%q", file.Name, file.Name))
		header.Set("Content-Type", file.ContentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return "", fmt.Errorf("Bug report upload failed: %v", err)
		}
		if _, err := part.Write([]byte(file.Data)); err != nil {
			return "", fmt.Errorf("Bug report upload failed: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("Bug report upload failed: %v", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return "", fmt.Errorf("Bug report upload failed: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if options.Token != "" {
		request.Header.Set("Authorization", "Bearer "+options.Token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("Bug report upload failed: %v", err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var payload struct {
		Ok        bool `json:"ok"`
		BugReport *struct {
			ID string `json:"id"`
		} `json:"bug_report"`
		Error       string `json:"error"`
		Description string `json:"description"`
	}
	// The payload stays zero when the body is not parseable (pi:
	// response.json().catch(() => null)).
	_ = json.Unmarshal(data, &payload)
	if response.StatusCode >= 200 && response.StatusCode < 300 && payload.Ok && payload.BugReport != nil && payload.BugReport.ID != "" {
		return payload.BugReport.ID, nil
	}
	detail := ""
	if !payload.Ok {
		detail = payload.Description
		if detail == "" {
			detail = payload.Error
		}
	}
	if detail == "" {
		detail = response.Status
	}
	return "", fmt.Errorf("Bug report upload failed: %s", detail)
}
