package storage

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// contentMD5 adds a Content-MD5 header to every request that carries a
// seekable body. Since aws-sdk-go-v2 s3 v1.73 the SDK no longer sends MD5
// (it uses CRC32 flexible checksums instead), and many S3-compatible
// stores still reject PutObject/UploadPart/DeleteObjects without it.
type contentMD5 struct{}

func (contentMD5) ID() string { return "BepaylotContentMD5" }

func (contentMD5) HandleBuild(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
	req, ok := in.Request.(*smithyhttp.Request)
	if !ok || req.Header.Get("Content-MD5") != "" {
		return next.HandleBuild(ctx, in)
	}
	body := req.GetStream()
	if body == nil || !req.IsStreamSeekable() {
		return next.HandleBuild(ctx, in)
	}
	h := md5.New()
	if _, err := io.Copy(h, body); err != nil {
		return middleware.BuildOutput{}, middleware.Metadata{}, fmt.Errorf("storage: content-md5: %w", err)
	}
	if err := req.RewindStream(); err != nil {
		return middleware.BuildOutput{}, middleware.Metadata{}, fmt.Errorf("storage: content-md5 rewind: %w", err)
	}
	req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(h.Sum(nil)))
	return next.HandleBuild(ctx, in)
}

func addContentMD5(stack *middleware.Stack) error {
	return stack.Build.Add(contentMD5{}, middleware.After)
}

// signPayloadSHA256 makes PutObject/UploadPart sign the real SHA-256 of the
// body. Over HTTPS the SDK otherwise sends "x-amz-content-sha256:
// UNSIGNED-PAYLOAD", which some S3-compatible stores hash-compare against
// the body and reject with XAmzContentSHA256Mismatch.
func signPayloadSHA256(stack *middleware.Stack) error {
	if _, ok := stack.Finalize.Get("ComputePayloadHash"); !ok {
		return nil
	}
	_, err := stack.Finalize.Swap("ComputePayloadHash", &v4.ComputePayloadSHA256{})
	return err
}
