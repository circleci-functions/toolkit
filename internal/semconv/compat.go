package semconv

import (
	"net/http"
)

func LegacyReqToAttributes(req *http.Request, attempt int) map[string]any {
	return map[string]any{
		"meta.type":                   "http_client",
		"http.scheme":                 req.URL.Scheme,
		"http.host":                   req.URL.Host,
		"http.target":                 req.URL.Path,
		"http.method":                 req.Method,
		"http.attempt":                attempt,
		"http.retry":                  attempt > 1,
		"http.url":                    req.URL.String(),
		"http.user_agent":             req.UserAgent(),
		"http.request_content_length": req.ContentLength,
	}
}

func LegacyRespToAttributes(res *http.Response) map[string]any {
	f := map[string]any{
		"http.status_code": res.StatusCode,
	}
	if cl := res.Header.Get("Content-Length"); cl != "" {
		f["http.response_content_length"] = cl
	}
	if ct := res.Header.Get("Content-Type"); ct != "" {
		f["http.response_content_type"] = ct
	}
	if ce := res.Header.Get("Content-Encoding"); ce != "" {
		f["http.response_content_encoding"] = ce
	}
	return f
}
