// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import (
	"errors"
	"strconv"
	"strings"
)

// httpResponse is the minimal parsed HTTP/1.1 response the client needs: the
// status code, the headers (lowercased names), and the body. The transport
// returns the raw response bytes; parseHTTPResponse decodes them.
type httpResponse struct {
	StatusCode int
	Status     string
	Headers    [][2]string
	Body       []byte
}

// header returns the first value for a header name (case-insensitive), or "".
func (r *httpResponse) header(name string) string {
	for _, h := range r.Headers {
		if strings.EqualFold(h[0], name) {
			return h[1]
		}
	}
	return ""
}

// parseHTTPResponse decodes an HTTP/1.1 response message into its status,
// headers, and body. It handles the status line, the header block (folding is
// not used by S3), and a Content-Length- or close-delimited body. It does not
// decode chunked transfer-encoding, since S3 responses to these operations are
// length-delimited; a chunked body is reported as an error.
func parseHTTPResponse(raw []byte) (*httpResponse, error) {
	sep := []byte("\r\n\r\n")
	idx := indexBytes(raw, sep)
	if idx < 0 {
		return nil, errors.New("nets3: malformed HTTP response: no header/body separator")
	}
	head := string(raw[:idx])
	body := raw[idx+len(sep):]

	lines := strings.Split(head, "\r\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil, errors.New("nets3: malformed HTTP response: empty status line")
	}
	// Status line: "HTTP/1.1 200 OK".
	statusLine := lines[0]
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return nil, errors.New("nets3: malformed HTTP status line: " + statusLine)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, errors.New("nets3: bad HTTP status code: " + parts[1])
	}
	resp := &httpResponse{StatusCode: code, Status: statusLine}

	for _, line := range lines[1:] {
		ci := strings.IndexByte(line, ':')
		if ci < 0 {
			return nil, errors.New("nets3: malformed HTTP header line: " + line)
		}
		name := strings.ToLower(strings.TrimSpace(line[:ci]))
		val := strings.TrimSpace(line[ci+1:])
		resp.Headers = append(resp.Headers, [2]string{name, val})
	}

	if strings.EqualFold(resp.header("transfer-encoding"), "chunked") {
		return nil, errors.New("nets3: chunked transfer-encoding is not supported by this parser")
	}

	// Trim the body to Content-Length when present, to drop any trailing bytes.
	if cl := resp.header("content-length"); cl != "" {
		n, err := strconv.Atoi(cl)
		if err != nil {
			return nil, errors.New("nets3: bad Content-Length: " + cl)
		}
		if n <= len(body) {
			body = body[:n]
		}
	}
	resp.Body = body
	return resp, nil
}

// indexBytes returns the index of the first occurrence of sub in b, or -1.
func indexBytes(b, sub []byte) int {
	return strings.Index(string(b), string(sub))
}

// s3Error inspects the response status and, for a 4xx/5xx, parses the S3 error
// XML body into an *Error. A 2xx/3xx returns nil. A non-2xx with no parseable
// error body still yields an *Error carrying the HTTP status.
func (r *httpResponse) s3Error() error {
	if r.StatusCode >= 200 && r.StatusCode < 300 {
		return nil
	}
	if e := parseS3Error(r.Body); e != nil {
		e.StatusCode = r.StatusCode
		return e
	}
	return &Error{StatusCode: r.StatusCode, Code: "HTTPError", Message: r.Status}
}
