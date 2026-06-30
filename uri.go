// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

import "strings"

// uriEncode percent-encodes s per the AWS SigV4 rules: the unreserved set
// A-Z a-z 0-9 - . _ ~ is passed through literally; every other byte is
// percent-encoded with uppercase hex. When encodeSlash is false, "/" is also
// passed through (used for the canonical URI path, where path separators stay
// literal); when true (query keys/values, and S3 object keys), "/" is encoded.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&0xf])
		}
	}
	return b.String()
}

const hexUpper = "0123456789ABCDEF"

// EncodeKeyPath turns an S3 object key into the canonical URI path component:
// a leading "/" followed by the key with every path segment URI-encoded but the
// "/" separators preserved. An empty key yields "/".
func EncodeKeyPath(key string) string {
	key = strings.TrimPrefix(key, "/")
	if key == "" {
		return "/"
	}
	return "/" + uriEncode(key, false)
}
