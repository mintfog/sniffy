// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/mintfog/sniffy/internal/update"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func r2TestStore(t *testing.T, handler http.HandlerFunc) *R2Store {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store, err := NewR2Store(R2Config{
		Account:   strings.Repeat("0", 32),
		Bucket:    "sniffy-test",
		AccessKey: "test-key",
		SecretKey: "test-secret",
	})
	require.NoError(t, err)
	store.client = s3.New(store.client.Options(), func(options *s3.Options) {
		options.BaseEndpoint = aws.String(server.URL)
		options.HTTPClient = server.Client()
		options.Retryer = retry.NewStandard(func(options *retry.StandardOptions) {
			options.MaxAttempts = 3
			options.Backoff = retry.BackoffDelayerFunc(func(int, error) (time.Duration, error) { return 0, nil })
		})
	})
	return store
}

func TestR2UploadRetriesContentAndPreservesMetadata(t *testing.T) {
	data := bytes.Repeat([]byte("制品"), 8192)
	var attempts atomic.Int32
	store := r2TestStore(t, func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		assert.NoError(t, err)
		assert.Equal(t, data, body)
		assert.Equal(t, http.MethodPut, request.Method)
		assert.Equal(t, "/sniffy-test/releases/v1.2.3/installer.exe", request.URL.Path)
		assert.Equal(t, int64(len(data)), request.ContentLength)
		assert.Equal(t, "application/octet-stream", request.Header.Get("Content-Type"))
		assert.Equal(t, immutableCacheControl, request.Header.Get("Cache-Control"))
		assert.Equal(t, `attachment; filename="installer.exe"`, request.Header.Get("Content-Disposition"))
		sum := md5.Sum(data)
		assert.Equal(t, base64.StdEncoding.EncodeToString(sum[:]), request.Header.Get("Content-MD5"))
		assert.Contains(t, request.Header.Get("Authorization"), "AWS4-HMAC-SHA256")
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "<Error><Code>SlowDown</Code><Message>稍后重试</Message></Error>")
			return
		}
		w.Header().Set("ETag", `"uploaded"`)
	})
	metadata := Metadata{
		ContentType:  "application/octet-stream",
		CacheControl: immutableCacheControl,
		Filename:     "installer.exe",
	}
	require.NoError(t, store.Put(t.Context(), "releases/v1.2.3/installer.exe", data, metadata))
	require.EqualValues(t, 2, attempts.Load())
}

func TestR2GetErrorsAndRetryLimit(t *testing.T) {
	for _, test := range []struct {
		code             string
		status, attempts int
		missing          bool
	}{
		{"NoSuchKey", 404, 1, true},
		{"NoSuchBucket", 404, 1, false},
		{"AccessDenied", 403, 1, false},
		{"InternalError", 500, 3, false},
	} {
		t.Run(test.code, func(t *testing.T) {
			var attempts atomic.Int32
			store := r2TestStore(t, func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.WriteHeader(test.status)
				fmt.Fprintf(w, "<Error><Code>%s</Code><Message>读取失败</Message></Error>", test.code)
			})
			_, err := store.Get(t.Context(), "artifact")
			require.Error(t, err)
			if test.missing {
				require.ErrorIs(t, err, fs.ErrNotExist)
			} else {
				require.NotErrorIs(t, err, fs.ErrNotExist)
			}
			require.EqualValues(t, test.attempts, attempts.Load())
		})
	}
}

func TestR2StatReadsHeadersOnly(t *testing.T) {
	data := []byte("制品内容")
	expected := objectInfo(data)
	store := r2TestStore(t, func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodHead, request.Method)
		switch {
		case strings.HasSuffix(request.URL.Path, "/missing"):
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(request.URL.Path, "/denied"):
			w.WriteHeader(http.StatusForbidden)
		default:
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("ETag", `"`+expected.ETag+`"`)
		}
	})
	info, err := store.Stat(t.Context(), "artifact")
	require.NoError(t, err)
	require.Equal(t, expected, info)
	_, err = store.Stat(t.Context(), "missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = store.Stat(t.Context(), "denied")
	require.Error(t, err)
	require.NotErrorIs(t, err, fs.ErrNotExist)
}

func TestR2GetCancellationAndReadback(t *testing.T) {
	started := make(chan struct{})
	store := r2TestStore(t, func(w http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/blocked") {
			close(started)
			<-request.Context().Done()
			return
		}
		w.Header().Set("Content-Length", "5")
		fmt.Fprint(w, "hello")
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := store.Get(ctx, "blocked")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("请求未到达测试服务")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("请求取消未返回")
	}
	data, err := store.Get(t.Context(), "artifact")
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), data)
}

func TestR2GetRejectsIncompleteResponse(t *testing.T) {
	store := r2TestStore(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "5")
		fmt.Fprint(w, "hi")
	})
	data, err := store.Get(t.Context(), "artifact")
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Nil(t, data)
}

func TestR2HTTPTimeout(t *testing.T) {
	store := r2TestStore(t, func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	})
	store.client = s3.New(store.client.Options(), func(options *s3.Options) {
		options.HTTPClient = &http.Client{Timeout: 20 * time.Millisecond}
		options.Retryer = aws.NopRetryer{}
	})
	_, err := store.Get(t.Context(), "artifact")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPublicProbeRejectsRedirectAndWrongLength(t *testing.T) {
	var fallbackRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		switch request.URL.Path {
		case "/redirect":
			http.Redirect(w, request, "/fallback", http.StatusFound)
		case "/fallback":
			fallbackRequests.Add(1)
		case "/wrong-length":
			w.Header().Set("Content-Length", "1")
		case "/good":
			w.Header().Set("Content-Length", "123")
		}
	}))
	defer server.Close()
	asset := update.Asset{Name: "installer.exe", URL: server.URL + "/good", Size: 123}
	require.NoError(t, verifyPublicAsset(t.Context(), server.Client(), asset))
	asset.URL = server.URL + "/wrong-length"
	require.Error(t, checkPublicAsset(t.Context(), server.Client(), asset))
	asset.URL = server.URL + "/redirect"
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	require.Error(t, verifyPublicAsset(ctx, server.Client(), asset))
	require.Zero(t, fallbackRequests.Load())
}
