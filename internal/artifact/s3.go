package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Secure          bool
}

type S3Store struct {
	client *minio.Client
	bucket string
	prefix string
}

func NewS3Store(configuration S3Config) (*S3Store, error) {
	if configuration.Endpoint == "" || configuration.Bucket == "" {
		return nil, errors.New("S3 endpoint and bucket are required")
	}
	client, err := minio.New(configuration.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(
			configuration.AccessKeyID,
			configuration.SecretAccessKey,
			configuration.SessionToken,
		),
		Secure: configuration.Secure,
		Region: configuration.Region,
	})
	if err != nil {
		return nil, err
	}
	return &S3Store{
		client: client,
		bucket: configuration.Bucket,
		prefix: strings.Trim(strings.TrimSpace(configuration.Prefix), "/"),
	}, nil
}

func (s *S3Store) objectName(key string) (string, error) {
	if !objectKeyPattern.MatchString(key) {
		return "", errors.New("invalid artifact object key")
	}
	if s.prefix == "" {
		return key, nil
	}
	return s.prefix + "/" + key, nil
}

func (s *S3Store) Put(ctx context.Context, key string, source io.Reader, size int64, mediaType string) error {
	name, err := s.objectName(key)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, s.bucket, name, source, size, minio.PutObjectOptions{ContentType: mediaType})
	return err
}

func (s *S3Store) Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	name, err := s.objectName(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	info, err := s.client.StatObject(ctx, s.bucket, name, minio.StatObjectOptions{})
	if err != nil {
		response := minio.ToErrorResponse(err)
		if response.StatusCode == 404 || response.Code == "NoSuchKey" || response.Code == "NoSuchObject" {
			return nil, ObjectInfo{}, ErrUnavailable
		}
		return nil, ObjectInfo{}, err
	}
	object, err := s.client.GetObject(ctx, s.bucket, name, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	return object, ObjectInfo{SizeBytes: info.Size, MediaType: info.ContentType}, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	name, err := s.objectName(key)
	if err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, s.bucket, name, minio.RemoveObjectOptions{})
}

func (s *S3Store) Health(ctx context.Context) error {
	if s == nil || s.client == nil || s.bucket == "" {
		return errors.New("S3 object store is not initialized")
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check S3 bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf("S3 bucket %q does not exist", s.bucket)
	}
	return nil
}
