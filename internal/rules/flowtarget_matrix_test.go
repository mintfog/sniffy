// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package rules

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mintfog/sniffy/internal/flow"
	"github.com/mintfog/sniffy/internal/pipeline"
	"github.com/mintfog/sniffy/internal/service"
)

// breakTargetFixture 与前端 fromFlow.test.ts 共用，见文件内 _comment。
type breakTargetFixture struct {
	Targets []struct {
		URL  string `json:"url"`
		Path string `json:"path"`
		Host string `json:"host"`
	} `json:"targets"`
	SameTarget []struct {
		Match string `json:"match"`
		A     string `json:"a"`
		B     string `json:"b"`
		Same  bool   `json:"same"`
	} `json:"sameTarget"`
}

func loadBreakTargetFixture(t *testing.T) breakTargetFixture {
	t.Helper()
	data, err := os.ReadFile("../pipeline/testdata/break_targets.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx breakTargetFixture
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}
	return fx
}

// wireReq 是客户端发到代理的一条请求：https 为 MITM 后的 origin-form，http 为普通代理的 absolute-form。
type wireReq struct {
	scheme, method, host, uri string
}

func (w wireReq) String() string { return w.method + " " + w.scheme + "://" + w.host + w.uri }

// live 按处理器的读取路径构造实时 flow：http.ReadRequest → 补全 scheme / host → BuildRequestFlow。
func (w wireReq) live(t *testing.T) *flow.Flow {
	t.Helper()
	target := w.uri
	if w.scheme == "http" {
		target = "http://" + w.host + w.uri
	}
	raw := w.method + " " + target + " HTTP/1.1\r\nHost: " + w.host + "\r\n\r\n"
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("%v: %v", w, err)
	}
	if req.URL.Scheme == "" {
		req.URL.Scheme = w.scheme
	}
	if req.URL.Host == "" {
		req.URL.Host = req.Host
	}
	req.RequestURI = ""
	f, err := flow.BuildRequestFlow(req, w.scheme)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// fromURL 是浏览器访问 rawURL 时实际发出的请求（空路径按 RFC 9112 发成 /）。
func fromURL(t *testing.T, method, rawURL string) wireReq {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return wireReq{scheme: u.Scheme, method: method, host: u.Host, uri: u.RequestURI()}
}

func (w wireReq) withURI(uri string) wireReq    { w.uri = uri; return w }
func (w wireReq) withHost(host string) wireReq  { w.host = host; return w }
func (w wireReq) withMethod(m string) wireReq   { w.method = m; return w }
func (w wireReq) path() string                  { return strings.SplitN(w.uri, "?", 2)[0] }
func (w wireReq) hostname() (name, port string) { return splitHostPort(w.host) }

func splitHostPort(host string) (string, string) {
	if h, p, err := net.SplitHostPort(host); err == nil {
		return h, ":" + p
	}
	return host, ""
}

type variant struct {
	name                    string
	req                     wireReq
	pathBP, hostBP, ruleHit bool
}

// variants 列出与 w 「同一请求 / 近似但不同」的请求及各产物的期望命中。
func variants(w wireReq) []variant {
	name, port := w.hostname()
	other := "other.test"
	if strings.Contains(name, ":") {
		other = "[::2]"
	} else if strings.HasPrefix(w.host, "[") {
		other = "[::2]"
	}
	bareName := name
	if strings.Contains(name, ":") {
		bareName = "[" + name + "]"
	}
	vs := []variant{
		{"原请求", w, true, true, true},
		{"换查询串", w.withURI(w.path() + "?zz=1"), true, true, true},
		{"主机大写", w.withHost(strings.ToUpper(w.host)), true, true, true},
		{"其他路径", w.withURI(strings.TrimSuffix(w.path(), "/") + "/zz"), false, true, false},
		{"其他主机", w.withHost(other + port), false, false, false},
		{"其他端口", w.withHost(bareName + ":9999"), false, false, false},
		{"其他方法", w.withMethod(http.MethodPost), true, true, false},
	}
	// absolute-form 允许省略根路径（GET http://a.test?q=1），与 / 是同一请求。
	if w.scheme == "http" && w.path() == "/" {
		vs = append(vs, variant{"省略根路径", w.withURI(strings.TrimPrefix(w.uri, "/")), true, true, true})
	}
	return vs
}

// harFlow 导入只含该 URL 的 HAR，返回导入后的会话 id。
func harFlow(t *testing.T, svc *service.Service, method, rawURL string) string {
	t.Helper()
	archive := service.HARArchive{Log: service.HARLog{Version: "1.2", Entries: []service.HAREntry{{
		StartedDateTime: time.Unix(1, 0).UTC(),
		Request:         service.HARRequest{Method: method, URL: rawURL, HTTPVersion: "HTTP/1.1", Headers: []service.HARPair{}},
		Response:        service.HARResponse{Status: 200, Headers: []service.HARPair{}, Content: service.HARContent{MimeType: "text/plain"}},
	}}}}
	data, _ := json.Marshal(archive)
	result, err := svc.ImportSessionArchive(strings.NewReader(string(data)))
	if err != nil || len(result.IDs) != 1 {
		t.Fatalf("导入 %s = %+v, %v", rawURL, result, err)
	}
	return result.IDs[0]
}

func breakHits(rule, match, url string) bool {
	b := pipeline.NewBreakpointManager(nil)
	if _, err := b.AddRuleWithMatch(rule, match, true, false, true); err != nil {
		panic(err)
	}
	return b.ShouldBreakFor(url, flow.PhaseRequest)
}

func ruleHits(t *testing.T, rule *service.InterceptRule, f *flow.Flow) bool {
	t.Helper()
	// 经 JSON 往返，即落盘重载后的形态。
	data, _ := json.Marshal(rule)
	var back service.InterceptRule
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	e := New(func() []*service.InterceptRule { return []*service.InterceptRule{&back} })
	return e.OnRequest(context.Background(), f).Kind == flow.Abort
}

// 从流量（实时抓到的 / HAR 导入的）生成的「此路径」「此域名」断点与规则，对同一请求的各种实际写法
// 都要命中，对近似但不同的请求都不能命中。
func TestFlowTargetMatrix(t *testing.T) {
	fx := loadBreakTargetFixture(t)
	targets := map[string][2]string{}
	for _, tg := range fx.Targets {
		targets[tg.URL] = [2]string{tg.Path, tg.Host}
	}
	type source struct {
		name string
		wire wireReq // 该流量对应的实际请求
		har  string  // 非空表示经 HAR 导入
	}
	var sources []source
	for _, w := range []wireReq{
		{"https", "GET", "a.test", "/x?id=1"},
		{"https", "GET", "a.test", "/?q=1"},
		{"https", "GET", "A.Test", "/"},
		{"https", "GET", "a.test:443", "/x"},
		{"https", "GET", "a.test:8443", "/x"},
		{"http", "GET", "a.test", "/x"},
		{"http", "GET", "a.test", "?q=1"},
		{"http", "GET", "a.test", "/?q=1"},
		{"https", "GET", "a.test", "/a%2Fb"},
		{"https", "GET", "a.test", "/%7Euser"},
		{"https", "GET", "a.test", "/a/../b"},
		{"https", "GET", "[::1]:8080", "/x"},
		{"https", "HEAD", "a.test", "/x"},
	} {
		sources = append(sources, source{name: "实时 " + w.String(), wire: w})
	}
	for _, raw := range []string{
		"https://a.test", "https://a.test?q=1", "https://a.test/x?id=1", "https://A.Test/",
		"https://a.test/~user", "https://a.test/a%2Fb", "http://[::1]:8080/x", "https://a.test?q=https://b.test/x",
		"http://a.test?q=1",
	} {
		sources = append(sources, source{name: "HAR " + raw, wire: fromURL(t, "GET", raw), har: raw})
	}

	for _, src := range sources {
		t.Run(src.name, func(t *testing.T) {
			svc := service.New(nil, nil, "", "")
			svc.StopRecording()
			var id, flowURL string
			if src.har != "" {
				id = harFlow(t, svc, src.wire.method, src.har)
				f, _ := svc.RawFlow(id)
				flowURL = f.Request.URL
			} else {
				f := src.wire.live(t)
				f.Response = &flow.Response{Status: 200, Header: map[string][]string{}}
				f.State = flow.StateCompleted
				svc.ImportFlowUpdated(f)
				id, flowURL = f.ID, f.Request.URL
			}
			tg, ok := targets[flowURL]
			if !ok {
				t.Fatalf("流量 URL %q 不在 break_targets.json 的 targets 里，补上它的期望目标", flowURL)
			}
			rule, err := svc.CreateRuleFromFlow(id, service.FlowRuleBlock, "n")
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range variants(src.wire) {
				live := v.req.live(t)
				if got := breakHits(tg[0], pipeline.BreakMatchPath, live.Request.URL); got != v.pathBP {
					t.Errorf("此路径 %q × %s %s：命中 = %v, want %v", tg[0], v.name, live.Request.URL, got, v.pathBP)
				}
				if got := breakHits(tg[1], pipeline.BreakMatchHost, live.Request.URL); got != v.hostBP {
					t.Errorf("此域名 %q × %s %s：命中 = %v, want %v", tg[1], v.name, live.Request.URL, got, v.hostBP)
				}
				if got := ruleHits(t, rule, live); got != v.ruleHit {
					t.Errorf("规则 %s × %s %s %s (Host %q, Path %q)：命中 = %v, want %v",
						conditionsString(rule), v.name, live.Request.Method, live.Request.URL, live.Request.Host, live.Request.Path, got, v.ruleHit)
				}
			}
		})
	}

	// sameTarget：菜单认作同一目标的两条规则，对任意请求的命中结果必须一致；认作不同的必须存在区分它们的请求。
	var probes []string
	for _, src := range sources {
		for _, v := range variants(src.wire) {
			probes = append(probes, v.req.live(t).Request.URL)
		}
	}
	for _, c := range fx.SameTarget {
		probes = append(probes, c.A, c.B)
	}
	for _, c := range fx.SameTarget {
		differ := ""
		for _, p := range probes {
			if breakHits(c.A, c.Match, p) != breakHits(c.B, c.Match, p) {
				differ = p
				break
			}
		}
		if c.Same && differ != "" {
			t.Errorf("sameTarget %q 模式下 %q 与 %q 应等价，但请求 %q 只命中其一", c.Match, c.A, c.B, differ)
		}
		if !c.Same && differ == "" {
			t.Errorf("sameTarget %q 模式下 %q 与 %q 应可区分，但没有请求能区分它们", c.Match, c.A, c.B)
		}
	}
}

func conditionsString(r *service.InterceptRule) string {
	var parts []string
	for _, c := range r.Conditions {
		parts = append(parts, fmt.Sprintf("%s=%q", c.Type, c.Value))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
