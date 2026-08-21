package base

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/go-resty/resty/v2"
)

// HTTPCapture holds one request/response pair captured from the driver.
type HTTPCapture struct {
	Method      string `json:"method"`
	URL         string `json:"url"`
	StatusCode  int    `json:"status_code"`
	ReqHeaders  http.Header
	RespHeaders http.Header
	ReqBody     string `json:"req_body,omitempty"`
	RespBody    string `json:"resp_body,omitempty"`
}

type reqBodySnapshot map[string]string

var (
	captMu     sync.Mutex
	captures   []HTTPCapture
	captOn     bool
	captHooked bool
	reqSnaps   reqBodySnapshot
)

// EnableHTTPCapture arms hooks on the Resty clients. Subsequent captures
// append to the in-memory buffer; call DisableHTTPCapture when done.
func EnableHTTPCapture() {
	captMu.Lock()
	defer captMu.Unlock()
	if captOn {
		return
	}
	if !captHooked {
		for _, c := range []*resty.Client{RestyClient, NoRedirectClient} {
			if c == nil {
				continue
			}
			hookClient(c)
		}
		captHooked = true
	}
	captOn = true
	captures = captures[:0]
	reqSnaps = make(reqBodySnapshot)
}

func DisableHTTPCapture() {
	captMu.Lock()
	defer captMu.Unlock()
	captOn = false
}

func UninstallHTTPCapture() {
	// resty v2 does not expose middleware removal; leave hooks installed,
	// disable via captOn flag.
	captMu.Lock()
	defer captMu.Unlock()
	captOn = false
}

func hookClient(c *resty.Client) {
	// Install our transport wrapper and the request-body snapshot
	// middleware on this resty Client.
	c.SetTransport(&captureTransport{})
	c.OnBeforeRequest(httpCaptureReq)
}

// captureTransport wraps the original transport to tee the response body
// into our capture buffer, so the caller (resty) still sees the full bytes
// while we also keep a snapshot.
type captureTransport struct{}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	captMu.Lock()
	on := captOn
	captMu.Unlock()

	if !on {
		// Bypass capture; just forward the request.
		return http.DefaultTransport.RoundTrip(req)
	}

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// Snapshot the request body that OnBeforeRequest stashed.
	reqBody := reqSnaps[snapshotKey(req.Method, req.URL.String())]
	delete(reqSnaps, snapshotKey(req.Method, req.URL.String()))

	// Build the capture record.
	cap := HTTPCapture{
		Method:      req.Method,
		URL:         req.URL.String(),
		StatusCode:  resp.StatusCode,
		ReqHeaders:  cloneHeader(req.Header),
		RespHeaders: resp.Header.Clone(),
		ReqBody:     reqBody,
	}

	captMu.Lock()
	idx := len(captures)
	captures = append(captures, cap)
	captMu.Unlock()

	// Tee the response body so both the caller and our capture see the
	// bytes. After reading, update the capture record with the body string.
	tee := &teeReader{
		r:        resp.Body,
		buf:      &bytes.Buffer{},
		captMu:   &captMu,
		captures: &captures,
		idx:      idx,
	}
	teeResp := *resp
	teeResp.Body = tee
	return &teeResp, nil
}

type teeReader struct {
	r        io.Reader
	buf      *bytes.Buffer
	captMu   *sync.Mutex
	captures *[]HTTPCapture
	idx      int
	done     bool
}

func (t *teeReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 && !t.done {
		t.buf.Write(p[:n])
	}
	if err == io.EOF || n == 0 {
		if !t.done {
			t.done = true
			t.captMu.Lock()
			if t.idx >= 0 && t.idx < len(*t.captures) {
				(*t.captures)[t.idx].RespBody = t.buf.String()
			}
			t.captMu.Unlock()
		}
	}
	return n, err
}

func (t *teeReader) Close() error {
	type closer interface{ Close() error }
	if c, ok := t.r.(closer); ok {
		return c.Close()
	}
	return nil
}

func snapshotKey(method, url string) string {
	return method + " " + url
}

func httpCaptureReq(c *resty.Client, r *resty.Request) error {
	captMu.Lock()
	defer captMu.Unlock()
	if !captOn {
		return nil
	}
	if r.Body == nil {
		return nil
	}
	var body string
	switch b := r.Body.(type) {
	case string:
		body = b
	case []byte:
		body = string(b)
	case io.Reader:
		var buf bytes.Buffer
		io.Copy(&buf, b)
		r.SetBody(bytes.NewReader(buf.Bytes()))
		body = buf.String()
	default:
		if jb, err := json.Marshal(b); err == nil {
			body = string(jb)
		}
	}
	if body != "" {
		reqSnaps[snapshotKey(r.Method, r.URL)] = body
	}
	return nil
}

func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func GetHTTPCaptures() []HTTPCapture {
	captMu.Lock()
	defer captMu.Unlock()
	return append([]HTTPCapture(nil), captures...)
}

func FormatCaptures(cs []HTTPCapture) string {
	if len(cs) == 0 {
		return "<no HTTP traffic captured>"
	}
	var b bytes.Buffer
	for i, c := range cs {
		if i > 0 {
			fmt.Fprintln(&b, "")
		}
		fmt.Fprintf(&b, "### %d. %s %s\n", i+1, c.Method, c.URL)
		fmt.Fprintf(&b, "Status: %d\n", c.StatusCode)
		if len(c.ReqHeaders) > 0 {
			fmt.Fprintf(&b, "Req Headers:\n%s\n", dumpHeader(c.ReqHeaders))
		}
		if len(c.RespHeaders) > 0 {
			fmt.Fprintf(&b, "Resp Headers:\n%s\n", dumpHeader(c.RespHeaders))
		}
		if c.ReqBody != "" {
			pretty := prettyJSON(c.ReqBody)
			if pretty != "" {
				fmt.Fprintf(&b, "Req:\n%s\n", pretty)
			} else {
				fmt.Fprintf(&b, "Req: %s\n", c.ReqBody)
			}
		}
		if c.RespBody != "" {
			pretty := prettyJSON(c.RespBody)
			if pretty != "" {
				fmt.Fprintf(&b, "Resp:\n%s\n", pretty)
			} else {
				fmt.Fprintf(&b, "Resp: %s\n", c.RespBody)
			}
		}
	}
	return b.String()
}

func dumpHeader(h http.Header) string {
	var b bytes.Buffer
	for k, v := range h {
		if len(v) == 0 {
			fmt.Fprintf(&b, "%s: (empty)\n", k)
		} else if len(v) == 1 {
			fmt.Fprintf(&b, "%s: %s\n", k, v[0])
		} else {
			fmt.Fprintf(&b, "%s: %v\n", k, v)
		}
	}
	return b.String()
}

func prettyJSON(s string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return ""
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(out)
}
