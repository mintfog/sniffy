// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			if !sameOriginOK(r) {
				fail(w, http.StatusForbidden, "cross-site request forbidden")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !s.tokenOK(r) {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) tokenOK(r *http.Request) bool {
	expect := []byte(s.token)
	if auth := r.Header.Get("Authorization"); len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") {
		return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(auth[7:])), expect) == 1
	}
	if r.URL.Path == "/api/ws" {
		return subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), expect) == 1
	}
	return false
}

func sameOriginOK(r *http.Request) bool {
	if !isLoopbackHostHeader(r.Host) {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func isLoopbackHostHeader(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
