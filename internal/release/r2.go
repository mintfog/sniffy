// Copyright 2026 The mintfog Authors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"slices"
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

// Store 的 Get 在对象不存在时返回 fs.ErrNotExist。
type Store interface {
	Get(context.Context, string) ([]byte, error)
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
		HTTPClient:                 &http.Client{Timeout: 10 * time.Minute},
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

// Put 在重试时复用 data，调用返回前调用方不得修改其内容。
func (store *R2Store) Put(ctx context.Context, key string, data []byte, metadata Metadata) error {
	input := &s3.PutObjectInput{
		Bucket:        aws.String(store.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
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
