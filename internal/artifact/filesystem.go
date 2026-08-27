package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var objectKeyPattern = regexp.MustCompile(`^[a-f0-9]{2}/[a-f0-9]{64}$`)

type FileStore struct {
	root string
}

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, errors.New("artifact filesystem root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("secure artifact root: %w", err)
	}
	return &FileStore{root: absolute}, nil
}

func (s *FileStore) path(key string) (string, error) {
	if !objectKeyPattern.MatchString(key) {
		return "", errors.New("invalid artifact object key")
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}

func (s *FileStore) Put(ctx context.Context, key string, source io.Reader, size int64, _ string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	written, copyErr := io.Copy(temporary, io.LimitReader(&contextReader{ctx: ctx, reader: source}, size+1))
	if copyErr == nil && written != size {
		copyErr = fmt.Errorf("artifact object size mismatch: wrote %d bytes, expected %d", written, size)
	}
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func (s *FileStore) Open(_ context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	path, err := s.path(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ObjectInfo{}, ErrUnavailable
	}
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, ObjectInfo{}, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, ObjectInfo{}, ErrUnavailable
	}
	return file, ObjectInfo{SizeBytes: info.Size()}, nil
}

func (s *FileStore) Delete(_ context.Context, key string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *FileStore) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.root == "" {
		return errors.New("artifact filesystem store is not initialized")
	}
	info, err := os.Stat(s.root)
	if err != nil {
		return fmt.Errorf("stat artifact filesystem root: %w", err)
	}
	if !info.IsDir() {
		return errors.New("artifact filesystem root is not a directory")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}
