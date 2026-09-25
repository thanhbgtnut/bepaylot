package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/thanhenti/bepaylot/internal/config"
)

// S3 implements ObjectStore on any S3-compatible endpoint.
type S3 struct {
	client  *s3.Client
	tm      *transfermanager.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3 connects to the configured endpoint. With CreateBucket set, a
// missing bucket is created (MinIO dev setups).
func NewS3(ctx context.Context, cfg config.S3Cfg) (*S3, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("storage: storage.s3.bucket is required")
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.AccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("storage: aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})
	partSize := cfg.PartSizeMB << 20
	if partSize < 5<<20 {
		partSize = 16 << 20
	}
	conc := cfg.UploadConcurrency
	if conc <= 0 {
		conc = 4
	}
	st := &S3{
		client: client,
		tm: transfermanager.New(client, func(o *transfermanager.Options) {
			o.PartSizeBytes = partSize
			o.Concurrency = conc
		}),
		presign: s3.NewPresignClient(client),
		bucket:  cfg.Bucket,
	}
	if cfg.CreateBucket {
		if err := st.ensureBucket(ctx); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func (s *S3) ensureBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucket}); err == nil {
		return nil
	}
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.bucket})
	var owned *s3types.BucketAlreadyOwnedByYou
	if err != nil && !errors.As(err, &owned) {
		return fmt.Errorf("storage: create bucket %s: %w", s.bucket, err)
	}
	return nil
}

// Put implements ObjectStore with a multipart upload whose memory use is
// bounded by part size × concurrency.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (ObjectInfo, error) {
	in := &transfermanager.UploadObjectInput{Bucket: &s.bucket, Key: &key, Body: r}
	if contentType != "" {
		in.ContentType = &contentType
	}
	if size >= 0 {
		in.ContentLength = aws.Int64(size)
	}
	out, err := s.tm.UploadObject(ctx, in)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("storage: put %s: %w", key, err)
	}
	info := ObjectInfo{Key: key, ContentType: contentType, Size: size}
	if out.ETag != nil {
		info.ETag = strings.Trim(*out.ETag, `"`)
	}
	if info.Size < 0 {
		if st, err := s.Stat(ctx, key); err == nil {
			info.Size = st.Size
		}
	}
	return info, nil
}

// Get implements ObjectStore.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, ObjectInfo{}, wrap(key, err)
	}
	return out.Body, ObjectInfo{Key: key, Size: aws.ToInt64(out.ContentLength), ETag: strings.Trim(aws.ToString(out.ETag), `"`), ContentType: aws.ToString(out.ContentType)}, nil
}

// Download implements ObjectStore with a concurrent ranged download.
func (s *S3) Download(ctx context.Context, key, path string) (int64, error) {
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	out, err := s.tm.DownloadObject(ctx, &transfermanager.DownloadObjectInput{Bucket: &s.bucket, Key: &key, WriterAt: f})
	cerr := f.Close()
	if err != nil {
		os.Remove(tmp)
		return 0, wrap(key, err)
	}
	if cerr != nil {
		os.Remove(tmp)
		return 0, cerr
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	return aws.ToInt64(out.ContentLength), nil
}

// Stat implements ObjectStore.
func (s *S3) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return ObjectInfo{}, wrap(key, err)
	}
	return ObjectInfo{Key: key, Size: aws.ToInt64(out.ContentLength), ETag: strings.Trim(aws.ToString(out.ETag), `"`), ContentType: aws.ToString(out.ContentType)}, nil
}

// Delete implements ObjectStore, in batches of 1000 keys.
func (s *S3) Delete(ctx context.Context, keys ...string) error {
	for len(keys) > 0 {
		n := min(len(keys), 1000)
		objs := make([]s3types.ObjectIdentifier, n)
		for i, k := range keys[:n] {
			objs[i] = s3types.ObjectIdentifier{Key: aws.String(k)}
		}
		if _, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &s.bucket, Delete: &s3types.Delete{Objects: objs, Quiet: aws.Bool(true)},
		}); err != nil {
			return fmt.Errorf("storage: delete: %w", err)
		}
		keys = keys[n:]
	}
	return nil
}

// DeletePrefix implements ObjectStore.
func (s *S3) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	total := 0
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return total, fmt.Errorf("storage: list %s: %w", prefix, err)
		}
		keys := make([]string, 0, len(page.Contents))
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
		if err := s.Delete(ctx, keys...); err != nil {
			return total, err
		}
		total += len(keys)
	}
	return total, nil
}

// PresignGet implements ObjectStore.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign %s: %w", key, err)
	}
	return req.URL, nil
}

func wrap(key string, err error) error {
	var nsk *s3types.NoSuchKey
	var nf *s3types.NotFound
	if errors.As(err, &nsk) || errors.As(err, &nf) {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	var api smithy.APIError
	if errors.As(err, &api) && (api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "NotFound") {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return fmt.Errorf("storage: %s: %w", key, err)
}
