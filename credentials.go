// Copyright (c) the go-ruby-net-s3/net-s3 authors
//
// SPDX-License-Identifier: BSD-3-Clause

package nets3

// Credentials are an AWS access key pair, optionally with a session token for
// temporary (STS) credentials. The session token, when present, is sent as the
// x-amz-security-token header and is included in the signed headers.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// NewCredentials builds a long-term credential pair.
func NewCredentials(accessKeyID, secretAccessKey string) Credentials {
	return Credentials{AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey}
}

// WithSessionToken returns a copy of the credentials carrying a temporary
// session token (STS), mirroring how a Ruby caller would layer it on.
func (c Credentials) WithSessionToken(token string) Credentials {
	c.SessionToken = token
	return c
}
