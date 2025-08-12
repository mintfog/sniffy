// Copyright 2025 The f-dong Authors
// SPDX-License-Identifier: Apache-2.0
// Use of this source code is governed by an Apache 2.0
// license that can be found in the LICENSE file.

package http

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mintfog/sniffy/capture/types"
	"github.com/mintfog/sniffy/plugins"
)

// RequestInterceptor HTTP请求拦截器
type RequestInterceptor struct {
	hookExecutor *plugins.HookExecutor
	logger       types.Logger
}

// NewRequestInterceptor 创建请求拦截器
func NewRequestInterceptor(hookExecutor *plugins.HookExecutor, logger types.Logger) *RequestInterceptor {
	return &RequestInterceptor{
		hookExecutor: hookExecutor,
		logger:       logger,
	}
}

// InterceptRequest 拦截HTTP请求
func (ri *RequestInterceptor) InterceptRequest(req *http.Request, conn types.Connection) (*http.Request, error) {
	if ri.hookExecutor == nil {
		return req, nil
	}

	// 读取请求体
	var requestBody []byte
	if req.Body != nil {
		var err error
		requestBody, err = io.ReadAll(req.Body)
		if err != nil {
			ri.logger.Error("读取请求体失败: %v", err)
		}
		req.Body.Close()
		
		// 重新创建请求体供后续使用
		req.Body = io.NopCloser(strings.NewReader(string(requestBody)))
	}

	// 创建拦截上下文
	interceptCtx := &plugins.InterceptContext{
		Request:         req,
		Connection:      conn,
		Timestamp:       time.Now(),
		RequestBody:     requestBody,
		RequestHeaders:  req.Header,
		Metadata:        make(map[string]interface{}),
	}

	// 执行请求拦截钩子
	ctx := context.Background()
	result, err := ri.hookExecutor.ExecuteRequestHooks(ctx, interceptCtx)
	if err != nil {
		ri.logger.Error("执行请求钩子失败: %v", err)
		return req, err
	}

	// 处理拦截结果
	if result != nil {
		if !result.Continue {
			ri.logger.Info("请求被插件终止: %s", result.Message)
			return nil, &InterceptError{Message: result.Message}
		}
		
		if result.Modified {
			ri.logger.Debug("请求已被插件修改: %s", result.Message)
		}
	}

	return req, nil
}

// InterceptResponse 拦截HTTP响应
func (ri *RequestInterceptor) InterceptResponse(resp *http.Response, req *http.Request, conn types.Connection) (*http.Response, error) {
	if ri.hookExecutor == nil {
		return resp, nil
	}

	// 读取响应体
	var responseBody []byte
	if resp.Body != nil {
		var err error
		responseBody, err = io.ReadAll(resp.Body)
		if err != nil {
			ri.logger.Error("读取响应体失败: %v", err)
		}
		resp.Body.Close()
		
		// 重新创建响应体供后续使用
		resp.Body = io.NopCloser(strings.NewReader(string(responseBody)))
	}

	// 创建拦截上下文
	interceptCtx := &plugins.InterceptContext{
		Request:         req,
		Response:        resp,
		Connection:      conn,
		Timestamp:       time.Now(),
		ResponseBody:    responseBody,
		ResponseHeaders: resp.Header,
		Metadata:        make(map[string]interface{}),
	}

	// 执行响应拦截钩子
	ctx := context.Background()
	result, err := ri.hookExecutor.ExecuteResponseHooks(ctx, interceptCtx)
	if err != nil {
		ri.logger.Error("执行响应钩子失败: %v", err)
		return resp, err
	}

	// 处理拦截结果
	if result != nil {
		if !result.Continue {
			ri.logger.Info("响应被插件终止: %s", result.Message)
			return nil, &InterceptError{Message: result.Message}
		}
		
		if result.Modified {
			ri.logger.Debug("响应已被插件修改: %s", result.Message)
		}
	}

	return resp, nil
}

// InterceptError 拦截错误
type InterceptError struct {
	Message string
}

func (e *InterceptError) Error() string {
	return e.Message
}