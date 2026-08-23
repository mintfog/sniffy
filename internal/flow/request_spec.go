// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package flow

// RequestSpec 描述一次由 UI 直接发起的请求(空白构造 / 编辑后重发)。
// 它与「照抄一条已捕获 flow」的重发分工明确:这里每个字段都由用户给定。
//
// 放在 flow 包而非 app:它和 Flow / Decision 一样是跨边界契约,两种 transport
// (internal/api 与 internal/desktop)都要引用,而它们都不该反向依赖装配层 app。
type RequestSpec struct {
	// Kind 标识收发模式,空串与 SpecKindHTTP 等价 —— 不认识该字段的旧客户端
	// (以及既有的「编辑后重发」)照旧走一次性 HTTP 往返。
	// SpecKindGraphQL 在后端与 HTTP 完全同路:body 已由前端合成好 JSON,这里只多打一个标签。
	Kind   string `json:"kind,omitempty"`
	Method string `json:"method"`
	URL    string `json:"url"`
	// Headers 是有序的头部列表,保留用户输入的大小写与重复项,并原样写线
	// (见 ApplyRequestToHTTP 的 RawHeaders 分支)。构造器承诺「所见即所发」,
	// 折叠成 map 会丢掉顺序与重复,让承诺落空。
	Headers [][2]string `json:"headers"`
	Body    string      `json:"body"`
	// FromID 是蓝本 flow,只作溯源标记,不影响发送内容。
	FromID string `json:"fromId,omitempty"`
	// ViaPipeline 决定这次请求是否经过插件 / 重写规则 / 断点。
	// 构造器默认关闭:手改过的请求再被规则改一遍、或撞上用户自己设的断点卡住,
	// 都会让「所见即所发」失效。原样重发则保持经过管道的既有语义。
	//
	// SpecKindWS 的作用域更窄:只覆盖逐帧的 OnWebSocketMessage,握手不过 OnRequest/OnResponse,
	// 因此规则与断点对 WS 不生效(见 App.OpenWebSocket)。
	ViaPipeline bool `json:"viaPipeline"`
}

// SpecKind 取值:构造器要按哪种模式收发。
const (
	SpecKindHTTP    = "http"
	SpecKindGraphQL = "graphql"
	SpecKindSSE     = "sse"
	SpecKindWS      = "ws"
)

// MaxComposeBodyBytes 是构造器请求体的字节上限。
//
// Body 会被整体读进内存、作为 Flow.Body 长期留在会话存储里,这条路径完全绕过响应侧的
// passthrough / bodycache,没有别的兜底。校验点必须在 app/service 边界而不是 REST 处理器:
// 桌面 Bridge 直接调 App.SendRequest,HTTP 请求体上限管不到它。
//
// 与 ComposeSeed 的蓝本上限(service.MaxComposeSeedBytes)配对:能载进编辑器的
// 一定发得出去,不会出现「预填了但一发就被拒」。
const MaxComposeBodyBytes = 8 << 20
