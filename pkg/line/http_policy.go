package line

import (
	"errors"
	"net/http"
)

// ErrRedirectRejected indicates an unknown outcome, never an authoritative
// mutation rejection. The original endpoint may have processed the request.
var ErrRedirectRejected = errors.New("LINE HTTP redirect rejected")

// doLINERequest enforces the policy at dispatch, including caller-supplied
// clients. Copy settings without mutating a client shared by other users.
func doLINERequest(client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	restricted := *client
	restricted.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := restricted.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, ErrRedirectRejected
	}
	return resp, nil
}
