// pi: test/bedrock-error-metadata.test.ts.
package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/ZoroSpace/pig/ai"

	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/ZoroSpace/pig/ai/types"
)

const (
	bedrockDiagnosticType = "bedrock_response_failure"
	bedrockValidationMsg  = "The provided model identifier is invalid."
	bedrockTestRequestID  = "11111111-2222-3333-4444-555555555555"
)

// makeBedrockServiceException builds what the SDK's handleError path throws
// for a non-2xx response: an OperationError wrapping a ResponseError carrying
// the HTTP metadata over the modeled AWS exception (pi: a ServiceException
// with name + $metadata).
func makeBedrockServiceException(t *testing.T, code string, status int, requestID string, unmodeled bool) error {
	t.Helper()
	var serviceErr error
	if unmodeled {
		serviceErr = &smithy.GenericAPIError{Code: code, Message: bedrockValidationMsg}
	} else {
		serviceErr = bedrockModeledException(code, bedrockValidationMsg)
	}
	return &smithy.OperationError{
		ServiceID:     "Bedrock Runtime",
		OperationName: "ConverseStream",
		Err: &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{
				StatusCode: status,
				Header:     http.Header{"X-Amzn-Requestid": []string{requestID}},
			}},
			Err: serviceErr,
		},
	}
}

// bedrockModeledException fabricates a modeled Bedrock exception with the
// given error code (pig tests cannot reference every modeled type by code; a
// validation exception matches the shape the SDK deserializes).
func bedrockModeledException(code, message string) error {
	return &bedrockTestException{code: code, message: message}
}

// bedrockTestException is a modeled-exception stand-in: it implements
// smithy.APIError without being a *smithy.GenericAPIError, so the adapter
// treats it as modeled.
type bedrockTestException struct {
	code    string
	message string
}

func (e *bedrockTestException) Error() string                 { return e.message }
func (e *bedrockTestException) ErrorCode() string             { return e.code }
func (e *bedrockTestException) ErrorMessage() string          { return e.message }
func (e *bedrockTestException) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestBedrockFailureDiagnostics(t *testing.T) {
	resetBedrockEnv(t)

	findDiagnostic := func(message types.AssistantMessage) *types.AssistantMessageDiagnostic {
		for i := range message.Diagnostics {
			if message.Diagnostics[i].Type == bedrockDiagnosticType {
				return &message.Diagnostics[i]
			}
		}
		return nil
	}

	runBedrock := func(t *testing.T, mock *bedrockMockClient) types.AssistantMessage {
		mock.install(t)
		s := streamBedrockConverse(context.Background(), bedrockTestModel(), bedrockTestContext(),
			&BedrockOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone}})
		return s.Result()
	}

	model := func() *types.Model {
		m := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]
		return &m
	}

	t.Run("records status, error code and request id for a non-2xx from client.send()", func(t *testing.T) {
		mock := &bedrockMockClient{err: makeBedrockServiceException(t, "ValidationException", 400, bedrockTestRequestID, false)}
		message := runBedrock(t, mock)

		if message.StopReason != types.StopReasonError {
			t.Fatalf("stopReason = %q", message.StopReason)
		}
		diagnostic := findDiagnostic(message)
		if diagnostic == nil {
			t.Fatal("expected a bedrock_response_failure diagnostic")
		}
		want := map[string]any{"status": 400, "errorCode": "ValidationException", "requestId": bedrockTestRequestID}
		if !errorsEqual(diagnostic.Details, want) {
			t.Fatalf("details = %v, want %v", diagnostic.Details, want)
		}
		if diagnostic.Error != nil {
			t.Fatalf("error = %+v, want unset", diagnostic.Error)
		}
		keys := []string{"details", "timestamp", "type"}
		gotKeys := diagnosticKeys(*diagnostic)
		if !slices.Equal(gotKeys, keys) {
			t.Fatalf("diagnostic keys = %v, want %v", gotKeys, keys)
		}
	})

	t.Run("leaves errorMessage untouched so retry classification is unaffected", func(t *testing.T) {
		mock := &bedrockMockClient{err: makeBedrockServiceException(t, "ValidationException", 400, bedrockTestRequestID, false)}
		message := runBedrock(t, mock)

		if message.ErrorMessage != "Validation error: "+bedrockValidationMsg {
			t.Fatalf("errorMessage = %q", message.ErrorMessage)
		}
	})

	t.Run("reports only the request id for a modeled mid-stream exception", func(t *testing.T) {
		// pi: the JS SDK throws a bare object literal here, so the code is
		// genuinely unavailable; pig constructs the no-metadata analog
		// directly. (The Go SDK models mid-stream exceptions, so a real
		// modeled frame would also report its code — declared difference.)
		mock := &bedrockMockClient{streamErr: errors.New("Too many requests, please wait.")}
		mock.metadata = bedrockResponseMetadata{HTTPStatusCode: 200, RequestID: bedrockTestRequestID}
		message := runBedrock(t, mock)

		if message.StopReason != types.StopReasonError {
			t.Fatalf("stopReason = %q", message.StopReason)
		}
		diagnostic := findDiagnostic(message)
		if diagnostic == nil {
			t.Fatal("expected a bedrock_response_failure diagnostic")
		}
		want := map[string]any{"requestId": bedrockTestRequestID}
		if !errorsEqual(diagnostic.Details, want) {
			t.Fatalf("details = %v, want %v", diagnostic.Details, want)
		}
	})

	t.Run("captures the error code for an unmodeled mid-stream error", func(t *testing.T) {
		// This branch throws a GenericAPIError named after the frame's
		// :error-code, exactly what the Go SDK produces for unmodeled
		// exception frames (pi: a real Error with name ModelStreamErrorException).
		mock := &bedrockMockClient{streamErr: &smithy.GenericAPIError{
			Code: "ModelStreamErrorException", Message: "Model stream terminated unexpectedly.",
		}}
		mock.metadata = bedrockResponseMetadata{HTTPStatusCode: 200, RequestID: bedrockTestRequestID}
		message := runBedrock(t, mock)

		diagnostic := findDiagnostic(message)
		if diagnostic == nil {
			t.Fatal("expected a bedrock_response_failure diagnostic")
		}
		want := map[string]any{"errorCode": "ModelStreamErrorException", "requestId": bedrockTestRequestID}
		if !errorsEqual(diagnostic.Details, want) {
			t.Fatalf("details = %v, want %v", diagnostic.Details, want)
		}
	})

	t.Run("does not report a transport failure name as a provider error code", func(t *testing.T) {
		// Real errors with informative names, but not AWS codes (pi: a
		// TimeoutError-named Error).
		mock := &bedrockMockClient{streamErr: errors.New("Connection timed out after 1000 ms")}
		mock.metadata = bedrockResponseMetadata{HTTPStatusCode: 200, RequestID: bedrockTestRequestID}
		message := runBedrock(t, mock)

		diagnostic := findDiagnostic(message)
		if diagnostic == nil {
			t.Fatal("expected a bedrock_response_failure diagnostic")
		}
		want := map[string]any{"requestId": bedrockTestRequestID}
		if !errorsEqual(diagnostic.Details, want) {
			t.Fatalf("details = %v, want %v", diagnostic.Details, want)
		}
	})

	t.Run("emits no diagnostic when the failure carries no provider metadata", func(t *testing.T) {
		mock := &bedrockMockClient{err: errors.New("socket hang up")}
		message := runBedrock(t, mock)

		if message.StopReason != types.StopReasonError {
			t.Fatalf("stopReason = %q", message.StopReason)
		}
		if message.ErrorMessage != "socket hang up" {
			t.Fatalf("errorMessage = %q", message.ErrorMessage)
		}
		if findDiagnostic(message) != nil {
			t.Fatal("expected no diagnostic")
		}
	})

	t.Run("emits no diagnostic for an aborted turn", func(t *testing.T) {
		mock := &bedrockMockClient{err: makeBedrockServiceException(t, "ValidationException", 400, bedrockTestRequestID, false)}
		mock.install(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := streamBedrockConverse(ctx, model(), bedrockTestContext(),
			&BedrockOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone}})
		message := s.Result()

		if message.StopReason != types.StopReasonAborted {
			t.Fatalf("stopReason = %q", message.StopReason)
		}
		if findDiagnostic(message) != nil {
			t.Fatal("expected no diagnostic")
		}
	})

	t.Run("drops header-derived values that exceed the length bound", func(t *testing.T) {
		longCode := repeatStr("E", 5000) + "Exception"
		longRequestID := repeatStr("R", 5000)
		mock := &bedrockMockClient{err: makeBedrockServiceException(t, longCode, 400, longRequestID, true)}
		message := runBedrock(t, mock)

		diagnostic := findDiagnostic(message)
		if diagnostic == nil {
			t.Fatal("expected a bedrock_response_failure diagnostic")
		}
		want := map[string]any{"status": 400}
		if !errorsEqual(diagnostic.Details, want) {
			t.Fatalf("details = %v, want %v", diagnostic.Details, want)
		}
	})

	t.Run("omits the SDK's Unknown placeholder instead of reporting it as a code", func(t *testing.T) {
		// The Go SDK's fallback when the response carried no x-amzn-errortype
		// is the code "UnknownError" (pi: name "Unknown"); it does not end in
		// "Exception", so no code is reported.
		mock := &bedrockMockClient{err: makeBedrockServiceException(t, "UnknownError", 403, bedrockTestRequestID, true)}
		message := runBedrock(t, mock)

		diagnostic := findDiagnostic(message)
		if diagnostic == nil {
			t.Fatal("expected a bedrock_response_failure diagnostic")
		}
		want := map[string]any{"status": 403, "requestId": bedrockTestRequestID}
		if !errorsEqual(diagnostic.Details, want) {
			t.Fatalf("details = %v, want %v", diagnostic.Details, want)
		}
	})
}

// bedrockTestModel re-reads the catalog fixture per test so mutations of one
// test's model cannot leak.
func bedrockTestModel() *types.Model {
	m := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]
	return &m
}

func diagnosticKeys(d types.AssistantMessageDiagnostic) []string {
	keys := []string{"type", "timestamp"}
	if d.Details != nil {
		keys = append(keys, "details")
	}
	if d.Error != nil {
		keys = append(keys, "error")
	}
	slices.Sort(keys)
	return keys
}

func errorsEqual(got map[string]any, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			return false
		}
		// JSON-decoded floats compare cleanly against literal ints here.
		if !anyEqual(gv, v) {
			return false
		}
	}
	return true
}

func anyEqual(a, b any) bool {
	return a == b
}

func repeatStr(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
