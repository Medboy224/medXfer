package api

import (
	"net/http"
	"os"
	"testing"
)

// testControlToken is the control token of every server created by these tests
// (MEDXFER_CONTROL_TOKEN, decision D0-2). Guard tests override it to check rejection.
const testControlToken = "7e57c0de7e57c0de7e57c0de7e57c0de7e57c0de7e57c0de7e57c0de7e57c0de"

func TestMain(m *testing.M) {
	_ = os.Setenv("MEDXFER_CONTROL_TOKEN", testControlToken)
	code := m.Run()
	if testConfigDir != "" {
		_ = os.RemoveAll(testConfigDir)
	}
	os.Exit(code)
}

// testAuth is the header a native client sends on control routes and /ws.
func testAuth() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+testControlToken)
	return h
}

// controlGet and controlPost are http.Get / http.Post with the control token.
func controlRequest(method, url string) (*http.Response, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header = testAuth()
	return http.DefaultClient.Do(req)
}

func controlGet(url string) (*http.Response, error)  { return controlRequest(http.MethodGet, url) }
func controlPost(url string) (*http.Response, error) { return controlRequest(http.MethodPost, url) }
