package snapshot

import (
	"context"
	"testing"
)

func TestNewStoreFromURLLocal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreFromURL(context.Background(), dir)
	if err != nil {
		t.Fatalf("NewStoreFromURL local: %v", err)
	}
	if _, ok := store.(*LocalStore); !ok {
		t.Fatalf("expected *LocalStore, got %T", store)
	}
}

func TestNewStoreFromURLS3(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping S3 store test in short mode")
	}
	store, err := NewStoreFromURL(context.Background(), "s3://mybucket/pfx")
	if err != nil {
		t.Fatalf("NewStoreFromURL s3: %v", err)
	}
	s3store, ok := store.(*S3SnapshotStore)
	if !ok {
		t.Fatalf("expected *S3SnapshotStore, got %T", store)
	}
	if s3store.bucket != "mybucket" {
		t.Fatalf("expected bucket mybucket, got %s", s3store.bucket)
	}
	if s3store.prefix != "pfx" {
		t.Fatalf("expected prefix pfx, got %s", s3store.prefix)
	}
}

func TestNewStoreFromURLEmpty(t *testing.T) {
	// An empty URL falls through to NewLocalStore(""), which errors on most
	// platforms because os.MkdirAll("") fails. Verify that NewStoreFromURL
	// surfaces the error rather than returning a nil or invalid store.
	_, err := NewStoreFromURL(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty URL, got nil")
	}
}
