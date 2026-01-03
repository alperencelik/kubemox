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
		errors        []error
		expectSuccess bool
		expectedCalls int
	}{
		{
			name:          "Retry on server closed idle connection",
			errors:        []error{errors.New("http: server closed idle connection")},
			expectSuccess: true,
			expectedCalls: 2,
		},
		{
			name:          "Retry on EOF",
			errors:        []error{errors.New("EOF")},
			expectSuccess: true,
			expectedCalls: 2,
		},
		{
			name:          "Retry on connection reset by peer",
			errors:        []error{errors.New("read: connection reset by peer")},
			expectSuccess: true,
			expectedCalls: 2,
		},
		{
			name:          "No retry on unknown error",
			errors:        []error{errors.New("unknown error")},
			expectSuccess: false,
			expectedCalls: 1,
		},
		{
			name:          "Success on first try",
			errors:        []error{},
			expectSuccess: true,
			expectedCalls: 1,
		},
		{
			name:          "Fail after max retries",
			errors:        []error{errors.New("EOF"), errors.New("EOF"), errors.New("EOF"), errors.New("EOF")},
			expectSuccess: false,
			expectedCalls: 4, // 1 initial + 3 retries
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRT := &MockRoundTripper{Errors: tt.errors}
			retryRT := &RetryRoundTripper{
				Transport: mockRT,
				Retries:   3,
			}

			req, _ := http.NewRequest("GET", "http://example.com", nil)
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
