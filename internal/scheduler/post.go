package scheduler

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

const maxPostRedirects = 6

// postFollow POSTs body to target and, when the server answers with a
// redirect, POSTs the same body to the new address. Go's own client turns a
// 301/302/303 into a GET and drops the body, which is what a project's
// http:// upload or scheduler address (redirected to https://) did: the server
// received an empty request and answered "no command". body is called once
// per attempt so it can rewind or reopen what it sends.
func postFollow(ctx context.Context, client *http.Client, target, contentType string, body func() (io.Reader, int64, error)) (*http.Response, error) {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for hop := 0; hop <= maxPostRedirects; hop++ {
		r, length, err := body()
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, r)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		req.ContentLength = length
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		switch resp.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if loc == "" {
				return nil, fmt.Errorf("redirect from %s without a Location", target)
			}
			next, err := req.URL.Parse(loc)
			if err != nil {
				return nil, fmt.Errorf("redirect from %s to %q: %w", target, loc, err)
			}
			target = next.String()
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("too many redirects from %s", target)
}
