// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import "testing"

func TestParseHTTPResponseOK(t *testing.T) {
	raw := []byte("HTTP/1.1 200 OK\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 5\r\n" +
		"ETag: \"abc\"\r\n" +
		"\r\n" +
		"hello-trailing-garbage")
	resp, err := parseHTTPResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if string(resp.Body) != "hello" { // trimmed to Content-Length
		t.Errorf("body = %q", resp.Body)
	}
	if resp.header("content-type") != "text/plain" || resp.header("ETAG") != `"abc"` {
		t.Errorf("headers wrong: %+v", resp.Headers)
	}
	if resp.header("absent") != "" {
		t.Errorf("absent header should be empty")
	}
}

func TestParseHTTPResponseNoContentLength(t *testing.T) {
	raw := []byte("HTTP/1.1 204 No Content\r\nx-amz-request-id: abc\r\n\r\n")
	resp, err := parseHTTPResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 204 || len(resp.Body) != 0 {
		t.Errorf("204 wrong: %d %q", resp.StatusCode, resp.Body)
	}
}

func TestParseHTTPResponseBodyShorterThanCL(t *testing.T) {
	// Content-Length larger than the body present: the body is taken as-is.
	raw := []byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
	resp, err := parseHTTPResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != "short" {
		t.Errorf("body = %q", resp.Body)
	}
}

func TestParseHTTPResponseErrors(t *testing.T) {
	cases := map[string][]byte{
		"no separator":    []byte("HTTP/1.1 200 OK\r\n"),
		"empty status":    []byte("\r\n\r\n"),
		"bad status line": []byte("garbage\r\n\r\n"),
		"bad code":        []byte("HTTP/1.1 nope OK\r\n\r\n"),
		"bad header":      []byte("HTTP/1.1 200 OK\r\nnocolon\r\n\r\n"),
		"bad CL":          []byte("HTTP/1.1 200 OK\r\nContent-Length: x\r\n\r\nbody"),
		"chunked":         []byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"),
	}
	for name, raw := range cases {
		if _, err := parseHTTPResponse(raw); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestS3ErrorFromStatus(t *testing.T) {
	// 2xx -> no error.
	good := &httpResponse{StatusCode: 200}
	if good.s3Error() != nil {
		t.Error("2xx should be nil")
	}
	// 4xx with parseable body.
	bad := &httpResponse{StatusCode: 404, Body: []byte(s3ErrorXML)}
	err := bad.s3Error()
	if err == nil {
		t.Fatal("expected error")
	}
	if e, ok := err.(*Error); !ok || e.Code != "NoSuchKey" || e.StatusCode != 404 {
		t.Errorf("error = %#v", err)
	}
	// 5xx with no parseable body -> synthesized HTTPError.
	raw := &httpResponse{StatusCode: 503, Status: "HTTP/1.1 503 Slow Down", Body: []byte("not xml")}
	err = raw.s3Error()
	e, ok := err.(*Error)
	if !ok || e.Code != "HTTPError" || e.StatusCode != 503 {
		t.Errorf("synthesized error = %#v", err)
	}
}
