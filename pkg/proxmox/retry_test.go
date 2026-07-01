package proxmox

import (
	"errors"
	"net/http"
	"testing"
)

type MockRoundTripper struct {
	Attempts int
	Errors   []error
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.Attempts < len(m.Errors) {
		err := m.Errors[m.Attempts]
		m.Attempts++
		return nil, err
	}
	m.Attempts++
	return &http.Response{StatusCode: 200}, nil
}

func TestRetryRoundTripper(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		errors        []error
		expectSuccess bool
		expectedCalls int
	}{
		{
			name:          "GET retry on server closed idle connection",
			method:        http.MethodGet,
			errors:        []error{errors.New("http: server closed idle connection")},
			expectSuccess: true,
			expectedCalls: 2,
		},
		{
			name:          "GET retry on EOF",
			method:        http.MethodGet,
			errors:        []error{errors.New("EOF")},
			expectSuccess: true,
			expectedCalls: 2,
		},
		{
			name:          "GET retry on connection reset by peer",
			method:        http.MethodGet,
			errors:        []error{errors.New("read: connection reset by peer")},
			expectSuccess: true,
			expectedCalls: 2,
		},
		{
			name:          "HEAD retry on EOF (idempotent, body-less)",
			method:        http.MethodHead,
			errors:        []error{errors.New("EOF")},
			expectSuccess: true,
			expectedCalls: 2,
		},
		{
			name:          "GET no retry on unknown error",
			method:        http.MethodGet,
			errors:        []error{errors.New("unknown error")},
			expectSuccess: false,
			expectedCalls: 1,
		},
		{
			name:          "GET success on first try",
			method:        http.MethodGet,
			errors:        []error{},
			expectSuccess: true,
			expectedCalls: 1,
		},
		{
			name:          "GET fail after max retries",
			method:        http.MethodGet,
			errors:        []error{errors.New("EOF"), errors.New("EOF"), errors.New("EOF"), errors.New("EOF")},
			expectSuccess: false,
			expectedCalls: 4, // 1 initial + 3 retries
		},
		// Regression guard: PVE uses POST for create/clone/start/stop/snapshot/delete.
		// Retrying POST after a connection drop can re-submit the request and
		// spawn duplicate tasks or VMs, so it must NOT retry.
		{
			name:          "POST no retry on EOF (mutative)",
			method:        http.MethodPost,
			errors:        []error{errors.New("EOF")},
			expectSuccess: false,
			expectedCalls: 1,
		},
		{
			name:          "POST no retry on server closed idle connection",
			method:        http.MethodPost,
			errors:        []error{errors.New("http: server closed idle connection")},
			expectSuccess: false,
			expectedCalls: 1,
		},
		{
			name:          "POST no retry on connection reset by peer",
			method:        http.MethodPost,
			errors:        []error{errors.New("connection reset by peer")},
			expectSuccess: false,
			expectedCalls: 1,
		},
		{
			name:          "PUT no retry on EOF (mutative config update)",
			method:        http.MethodPut,
			errors:        []error{errors.New("EOF")},
			expectSuccess: false,
			expectedCalls: 1,
		},
		{
			name:          "DELETE no retry on EOF (mutative)",
			method:        http.MethodDelete,
			errors:        []error{errors.New("EOF")},
			expectSuccess: false,
			expectedCalls: 1,
		},
		{
			name:          "POST success on first try",
			method:        http.MethodPost,
			errors:        []error{},
			expectSuccess: true,
			expectedCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRT := &MockRoundTripper{Errors: tt.errors}
			retryRT := &RetryRoundTripper{
				Transport: mockRT,
				Retries:   3,
			}

			req, _ := http.NewRequest(tt.method, "http://example.com", nil)
			resp, err := retryRT.RoundTrip(req)

			if tt.expectSuccess {
				if err != nil {
					t.Errorf("Expected success, got error: %v", err)
				}
				if resp.StatusCode != 200 {
					t.Errorf("Expected status 200, got %d", resp.StatusCode)
				}
			} else {
				if err == nil {
					t.Errorf("Expected error, got success")
				}
			}

			if mockRT.Attempts != tt.expectedCalls {
				t.Errorf("Expected %d calls, got %d", tt.expectedCalls, mockRT.Attempts)
			}
		})
	}
}
