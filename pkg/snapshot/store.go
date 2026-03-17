// Package snapshot manages Firecracker microVM snapshots.
package snapshot

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// NewStoreFromURL creates a SnapshotStore from a URL string.
// Supported schemes:
//   - "s3://bucket/prefix" → S3SnapshotStore (credentials from AWS default chain)
//   - local path (no scheme) → LocalStore
func NewStoreFromURL(ctx context.Context, rawURL string) (SnapshotStore, error) {
	if strings.HasPrefix(rawURL, "s3://") {
		rest := strings.TrimPrefix(rawURL, "s3://")
		parts := strings.SplitN(rest, "/", 2)
		bucket := parts[0]
		prefix := ""
		if len(parts) == 2 {
			prefix = parts[1]
		}
		// Use an intermediate variable so that a nil *S3SnapshotStore is not
		// wrapped into a non-nil SnapshotStore interface on error.
		s, err := NewS3Store(ctx, bucket, prefix)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	// Same pattern for LocalStore: avoid the nil-interface trap.
	ls, err := NewLocalStore(rawURL)
	if err != nil {
		return nil, err
	}
	return ls, nil
}

// SnapshotStore is the interface for storing and retrieving Firecracker snapshots.
type SnapshotStore interface {
	// Push uploads a snapshot directory to the store.
	Push(ctx context.Context, name, localDir string) error
	// Pull downloads a snapshot from the store to localDir.
	Pull(ctx context.Context, name, localDir string) error
	// List returns the names of available snapshots.
	List(ctx context.Context) ([]string, error)
	// Delete removes a snapshot from the store.
	Delete(ctx context.Context, name string) error
}

// LocalStore implements SnapshotStore backed by a local directory.
type LocalStore struct {
	root string
}

// NewLocalStore creates a LocalStore rooted at root.
func NewLocalStore(root string) (*LocalStore, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("failed to create snapshot store directory: %w", err)
	}
	return &LocalStore{root: root}, nil
}

// Push copies localDir into root/name.
func (s *LocalStore) Push(_ context.Context, name, localDir string) error {
	dest := filepath.Join(s.root, name)
	return copyDir(localDir, dest)
}

// Pull copies root/name into localDir.
func (s *LocalStore) Pull(_ context.Context, name, localDir string) error {
	src := filepath.Join(s.root, name)
	return copyDir(src, localDir)
}

// List returns the names of snapshot directories in root.
func (s *LocalStore) List(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Delete removes the snapshot directory.
func (s *LocalStore) Delete(_ context.Context, name string) error {
	return os.RemoveAll(filepath.Join(s.root, name))
}

// copyDir recursively copies src into dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, src)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}
