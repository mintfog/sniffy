// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

var (
	accountIDPattern  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
)

type Metadata struct {
	ContentType  string
	CacheControl string
	Filename     string
}

// ObjectInfo 的 ETag 不含引号；单次 PUT 上传的对象，其 ETag 即内容 MD5 的十六进制值。
type ObjectInfo struct {
	Size int64
	ETag string
}

func objectInfo(data []byte) ObjectInfo {
	sum := md5.Sum(data)
	return ObjectInfo{Size: int64(len(data)), ETag: hex.EncodeToString(sum[:])}
}

// Store 的 Get 与 Stat 在对象不存在时返回 fs.ErrNotExist。
type Store interface {
	Get(context.Context, string) ([]byte, error)
	Stat(context.Context, string) (ObjectInfo, error)
	Put(context.Context, string, []byte, Metadata) error
}

// R2Config 使用显式提供的凭据，不读取本机 AWS 配置。
type R2Config struct {
	Account   string
	Bucket    string
	AccessKey string
	SecretKey string
}

type R2Store struct {
	client *s3.Client
	bucket string
}

// NewR2Store 校验配置并创建带超时及重试的客户端，不发起网络请求。
func NewR2Store(config R2Config) (*R2Store, error) {
	if !accountIDPattern.MatchString(config.Account) || !bucketNamePattern.MatchString(config.Bucket) {
		return nil, fmt.Errorf("请配置有效的 R2_ACCOUNT_ID 和 R2_BUCKET")
	}
	if config.AccessKey == "" || config.SecretKey == "" {
		return nil, fmt.Errorf("请配置 AWS_ACCESS_KEY_ID 和 AWS_SECRET_ACCESS_KEY")
	}
	client := s3.NewFromConfig(aws.Config{
		Region:                     "auto",
		Credentials:                credentials.NewStaticCredentialsProvider(config.AccessKey, config.SecretKey, ""),
		HTTPClient:                 &http.Client{Timeout: 2 * time.Minute},
		RetryMaxAttempts:           3,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}, func(options *s3.Options) {
		options.BaseEndpoint = aws.String("https://" + config.Account + ".r2.cloudflarestorage.com")
		options.UsePathStyle = true
	})
	return &R2Store{client: client, bucket: config.Bucket}, nil
}

// Get 读取完整对象；仅对象缺失映射为 fs.ErrNotExist，桶或权限错误保留原错误。
func (store *R2Store) Get(ctx context.Context, key string) ([]byte, error) {
	object, err := store.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(key),
	})
	if hasAPIErrorCode(err, "NoSuchKey", "NotFound") {
		return nil, fmt.Errorf("R2 对象 %s：%w", key, fs.ErrNotExist)
	}
	if err != nil {
		return nil, fmt.Errorf("读取 R2 对象 %s：%w", key, err)
	}
	defer object.Body.Close()
	data, err := io.ReadAll(object.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 R2 对象 %s：%w", key, err)
	}
	return data, nil
}

// Stat 只发 HEAD 请求；对象缺失的映射规则与 Get 相同。
func (store *R2Store) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	object, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(key),
	})
	if hasAPIErrorCode(err, "NoSuchKey", "NotFound") {
		return ObjectInfo{}, fmt.Errorf("R2 对象 %s：%w", key, fs.ErrNotExist)
	}
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("读取 R2 对象信息 %s：%w", key, err)
	}
	return ObjectInfo{
		Size: aws.ToInt64(object.ContentLength),
		ETag: strings.Trim(aws.ToString(object.ETag), `"`),
	}, nil
}

// Put 在重试时复用 data，调用返回前调用方不得修改其内容。
// Content-MD5 由 R2 在服务端校验，内容在传输中损坏时上传直接失败。
func (store *R2Store) Put(ctx context.Context, key string, data []byte, metadata Metadata) error {
	sum := md5.Sum(data)
	input := &s3.PutObjectInput{
		Bucket:        aws.String(store.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
		ContentMD5:    aws.String(base64.StdEncoding.EncodeToString(sum[:])),
		ContentType:   aws.String(metadata.ContentType),
		CacheControl:  aws.String(metadata.CacheControl),
	}
	if metadata.Filename != "" {
		input.ContentDisposition = aws.String(`attachment; filename="` + metadata.Filename + `"`)
	}
	_, err := store.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("上传 R2 对象 %s：%w", key, err)
	}
	return nil
}

func hasAPIErrorCode(err error, codes ...string) bool {
	var apiError smithy.APIError
	if !errors.As(err, &apiError) {
		return false
	}
	return slices.Contains(codes, apiError.ErrorCode())
}
