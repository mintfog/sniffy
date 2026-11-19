// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/mintfog/sniffy/internal/service"
)

// recordingTunBackend 记录 service 对虚拟网卡运行时的开启与直通名单调用。closeErr 不为 nil 时网卡关不掉。
type recordingTunBackend struct {
	mu       sync.Mutex
	calls    []call
	bypass   []service.DecryptBypassEntry
	closeErr error
}

func (b *recordingTunBackend) record(method string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call{Method: method, Args: args})
}

func (b *recordingTunBackend) recorded() []call {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

func (b *recordingTunBackend) Start(context.Context, service.TunOptions) (service.TunDevice, error) {
	b.record("Start")
	return &stubTunDevice{closeErr: b.closeErr, done: make(chan struct{})}, nil
}

func (b *recordingTunBackend) SetCATrusted(bool)                             {}
func (b *recordingTunBackend) TunStatusChanged(prev, next service.TunStatus) {}

func (b *recordingTunBackend) DecryptBypass() []service.DecryptBypassEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.bypass)
}

func (b *recordingTunBackend) ForgetDecryptBypass(host string) {
	b.record("ForgetDecryptBypass", host)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bypass = slices.DeleteFunc(b.bypass, func(e service.DecryptBypassEntry) bool { return e.Host == host })
}

func (b *recordingTunBackend) ClearDecryptBypass() {
	b.record("ClearDecryptBypass")
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bypass = nil
}

type stubTunDevice struct {
	closeErr error
	once     sync.Once
	done     chan struct{}
}

func (d *stubTunDevice) Name() string          { return "sniffy0" }
func (d *stubTunDevice) Done() <-chan struct{} { return d.done }
func (d *stubTunDevice) Err() error            { return nil }

func (d *stubTunDevice) Close() error {
	if d.closeErr != nil {
		return d.closeErr
	}
	d.once.Do(func() { close(d.done) })
	return nil
}

// newTunServer 是接入了 backend 的 newTestServer，测试结束时关闭虚拟网卡。
func newTunServer(t *testing.T, backend *recordingTunBackend, opts ...serverOpt) (*Server, *http.ServeMux) {
	t.Helper()
	s, mux := newTestServer(t, opts...)
	s.svc.SetTunBackend(backend)
	t.Cleanup(func() { _, _ = s.svc.StopTun() })
	return s, mux
}

func TestTunEndpointStartStop(t *testing.T) {
	t.Parallel()
	backend := &recordingTunBackend{}
	s, mux := newTunServer(t, backend)

	var st service.TunStatus
	decodeEnvelope(t, do(t, mux, http.MethodGet, "/api/tun", "")).into(t, &st)
	if st.State != service.TunStateOff {
		t.Fatalf("初始状态 = %+v", st)
	}

	rec := do(t, mux, http.MethodPost, "/api/tun", `{"enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("开启 = %d (%s)", rec.Code, rec.Body.String())
	}
	decodeEnvelope(t, rec).into(t, &st)
	if st.State != service.TunStateStarting {
		t.Errorf("开启后立即返回的状态 = %+v，期望 starting", st)
	}
	if st, _ := s.svc.WaitTunSettled(context.Background()); st.State != service.TunStateRunning {
		t.Fatalf("开启结果 = %+v", st)
	}
	decodeEnvelope(t, do(t, mux, http.MethodGet, "/api/tun", "")).into(t, &st)
	if st.State != service.TunStateRunning || st.Name != "sniffy0" {
		t.Errorf("查询到的状态 = %+v", st)
	}
	// 根证书没有被系统信任（测试的 service 没有根证书），透明连接上的 TLS 一律盲转发。
	if !st.CAUntrusted {
		t.Errorf("状态 = %+v，期望带 caUntrusted", st)
	}

	rec = do(t, mux, http.MethodPost, "/api/tun", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("关闭 = %d (%s)", rec.Code, rec.Body.String())
	}
	decodeEnvelope(t, rec).into(t, &st)
	if st.State != service.TunStateOff {
		t.Errorf("关闭后状态 = %+v", st)
	}
	assertCalls(t, backend.recorded(), call{Method: "Start"})
}

func TestTunEndpointUnavailable(t *testing.T) {
	t.Parallel()
	_, mux := newTestServer(t)
	if rec := do(t, mux, http.MethodPost, "/api/tun", `{"enabled":true}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("没有接入虚拟网卡时开启 = %d，期望 501", rec.Code)
	}
	if rec := do(t, mux, http.MethodPost, "/api/tun", `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Errorf("没有接入虚拟网卡时关闭 = %d，期望 200", rec.Code)
	}
}

func TestTunEndpointRejectsBadBody(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "{}", `{"enabled":"yes"}`, "{", `{"enabled":null}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			backend := &recordingTunBackend{}
			_, mux := newTunServer(t, backend)
			if rec := do(t, mux, http.MethodPost, "/api/tun", body); rec.Code != http.StatusBadRequest {
				t.Errorf("状态码 = %d，期望 400", rec.Code)
			}
			assertNoCalls(t, backend.recorded())
		})
	}
}

// TestTunEndpointWhileStopping：网卡没能及时关掉时关闭返回 500 并带上原因，此时开启返回 409。
func TestTunEndpointWhileStopping(t *testing.T) {
	t.Parallel()
	backend := &recordingTunBackend{closeErr: errors.New("helper 没有在 10s 内拆掉虚拟网卡 sniffy0")}
	s, mux := newTunServer(t, backend)
	if _, err := s.svc.StartTun(); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.svc.WaitTunSettled(context.Background()); st.State != service.TunStateRunning {
		t.Fatalf("开启结果 = %+v", st)
	}
	rec := do(t, mux, http.MethodPost, "/api/tun", `{"enabled":false}`)
	if rec.Code != http.StatusInternalServerError || decodeEnvelope(t, rec).Message != backend.closeErr.Error() {
		t.Errorf("关闭 = %d (%s)，期望 500 并带上原因", rec.Code, rec.Body.String())
	}
	if rec := do(t, mux, http.MethodPost, "/api/tun", `{"enabled":true}`); rec.Code != http.StatusConflict {
		t.Errorf("网卡还在时开启 = %d，期望 409", rec.Code)
	}
}

func TestDecryptBypassEndpoints(t *testing.T) {
	t.Parallel()
	pinned := service.DecryptBypassEntry{Host: "pinned.test:443", Reason: "cert_rejected", Since: "2026-11-18T00:00:00Z"}
	mail := service.DecryptBypassEntry{Host: "mail.test:993", Reason: "not_http", Since: "2026-11-18T00:00:01Z"}
	v6 := service.DecryptBypassEntry{Host: "[2001:db8::1]:443", Reason: "origin_rejected", Since: "2026-11-18T00:00:02Z"}
	backend := &recordingTunBackend{bypass: []service.DecryptBypassEntry{pinned, mail, v6}}
	_, mux := newTunServer(t, backend)

	list := func(rec *httptest.ResponseRecorder) []service.DecryptBypassEntry {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d (%s)", rec.Code, rec.Body.String())
		}
		var got []service.DecryptBypassEntry
		decodeEnvelope(t, rec).into(t, &got)
		return got
	}
	if got := list(do(t, mux, http.MethodGet, "/api/tun/decrypt-bypass", "")); !slices.Equal(got, backend.bypass) {
		t.Errorf("直通名单 = %+v", got)
	}
	if got := list(do(t, mux, http.MethodDelete, "/api/tun/decrypt-bypass/pinned.test:443", "")); !slices.Equal(got, []service.DecryptBypassEntry{mail, v6}) {
		t.Errorf("删掉一条后 = %+v", got)
	}
	if got := list(do(t, mux, http.MethodDelete, "/api/tun/decrypt-bypass/%5B2001:db8::1%5D:443", "")); !slices.Equal(got, []service.DecryptBypassEntry{mail}) {
		t.Errorf("删掉 IPv6 主机后 = %+v", got)
	}
	if got := list(do(t, mux, http.MethodDelete, "/api/tun/decrypt-bypass", "")); len(got) != 0 {
		t.Errorf("清空后 = %+v", got)
	}
	assertCalls(t, backend.recorded(),
		call{Method: "ForgetDecryptBypass", Args: []any{"pinned.test:443"}},
		call{Method: "ForgetDecryptBypass", Args: []any{"[2001:db8::1]:443"}},
		call{Method: "ClearDecryptBypass"},
	)
}

func TestDecryptBypassHostRejectsBadPath(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		path string
		code int
	}{
		{"/api/tun/decrypt-bypass/", http.StatusBadRequest},
		{"/api/tun/decrypt-bypass/pinned.test", http.StatusBadRequest},
		{"/api/tun/decrypt-bypass/pinned.test:443/retry", http.StatusNotFound},
	} {
		t.Run(c.path, func(t *testing.T) {
			t.Parallel()
			backend := &recordingTunBackend{}
			_, mux := newTunServer(t, backend)
			if rec := do(t, mux, http.MethodDelete, c.path, ""); rec.Code != c.code {
				t.Errorf("状态码 = %d，期望 %d", rec.Code, c.code)
			}
			assertNoCalls(t, backend.recorded())
		})
	}
}

// TestTunEndpointsReadMethodsHaveNoSideEffects：GET / HEAD 到达不了开启与清除，其余方法 405。
func TestTunEndpointsReadMethodsHaveNoSideEffects(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/api/tun", "/api/tun/decrypt-bypass", "/api/tun/decrypt-bypass/pinned.test:443"} {
		for _, m := range allMethods {
			if m == http.MethodPost && path == "/api/tun" || m == http.MethodDelete && path != "/api/tun" {
				continue
			}
			t.Run(m+" "+path, func(t *testing.T) {
				t.Parallel()
				backend := &recordingTunBackend{}
				_, mux := newTunServer(t, backend)
				rec := do(t, mux, m, path, `{"enabled":true}`)
				assertNoCalls(t, backend.recorded())
				if !isReadMethod(m) && rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("状态码 = %d，期望 405", rec.Code)
				}
			})
		}
	}
}

// TestTunEndpointsRequireToken：开关与直通名单的端点同样只认 Bearer token，未认证的请求碰不到运行时。
func TestTunEndpointsRequireToken(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/tun", ""},
		{http.MethodPost, "/api/tun", `{"enabled":true}`},
		{http.MethodGet, "/api/tun/decrypt-bypass", ""},
		{http.MethodDelete, "/api/tun/decrypt-bypass", ""},
		{http.MethodDelete, "/api/tun/decrypt-bypass/pinned.test:443", ""},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			t.Parallel()
			backend := &recordingTunBackend{}
			s, mux := newTunServer(t, backend, withToken("secret"))
			h := s.authMiddleware(mux)
			if rec := do(t, h, c.method, c.path, c.body); rec.Code != http.StatusUnauthorized {
				t.Errorf("未带 token = %d，期望 401", rec.Code)
			}
			assertNoCalls(t, backend.recorded())
			if rec := do(t, h, c.method, c.path, c.body, withHeader("Authorization", "Bearer secret")); rec.Code != http.StatusOK {
				t.Errorf("带 token = %d (%s)，期望 200", rec.Code, rec.Body.String())
			}
		})
	}
}
