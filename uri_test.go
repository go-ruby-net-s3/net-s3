// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import "testing"

func TestURIEncode(t *testing.T) {
	cases := []struct {
		in          string
		encodeSlash bool
		want        string
	}{
		{"abcXYZ09-._~", true, "abcXYZ09-._~"}, // unreserved passes through
		{"a/b", false, "a/b"},                  // slash kept
		{"a/b", true, "a%2Fb"},                 // slash encoded
		{"a b", true, "a%20b"},                 // space
		{"a+b", true, "a%2Bb"},                 // plus
		{"=&?", true, "%3D%26%3F"},
		{"é", true, "%C3%A9"}, // multi-byte UTF-8, uppercase hex
	}
	for _, c := range cases {
		if got := uriEncode(c.in, c.encodeSlash); got != c.want {
			t.Errorf("uriEncode(%q, %v) = %q, want %q", c.in, c.encodeSlash, got, c.want)
		}
	}
}

func TestEncodeKeyPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"key.txt", "/key.txt"},
		{"/key.txt", "/key.txt"},
		{"a/b/c.txt", "/a/b/c.txt"},
		{"my key.txt", "/my%20key.txt"},
		{"dir/my key.txt", "/dir/my%20key.txt"},
	}
	for _, c := range cases {
		if got := EncodeKeyPath(c.in); got != c.want {
			t.Errorf("EncodeKeyPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
