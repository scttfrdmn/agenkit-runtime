package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	s3manager "github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3SnapshotStore implements SnapshotStore backed by Amazon S3.
//
// Key layout:
//
//	{prefix}/snapshots/{name}/vm.snap  — snapshot state file
//	{prefix}/snapshots/{name}/vm.mem   — memory file
//	{prefix}/manifests/{id}.json       — migration manifests
type S3SnapshotStore struct {
	bucket     string
	prefix     string
	client     *s3.Client
	uploader   *s3manager.Uploader
	downloader *s3manager.Downloader
}

// NewS3Store creates an S3SnapshotStore for the given bucket and key prefix.
// AWS credentials are loaded from the default credential chain (env vars,
// ~/.aws/credentials, IAM instance role, etc.).
func NewS3Store(ctx context.Context, bucket, prefix string) (*S3SnapshotStore, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %w", err)
	}
	client := s3.NewFromConfig(cfg)
	return &S3SnapshotStore{
		bucket:     bucket,
		prefix:     prefix,
		client:     client,
		uploader:   s3manager.NewUploader(client),
		downloader: s3manager.NewDownloader(client),
	}, nil
}

// snapshotPrefix returns the S3 key prefix for the named snapshot.
func (s *S3SnapshotStore) snapshotPrefix(name string) string {
	if s.prefix == "" {
		return "snapshots/" + name + "/"
	}
	return s.prefix + "/snapshots/" + name + "/"
}

// snapshotsRootPrefix returns the S3 key prefix under which all snapshots live.
func (s *S3SnapshotStore) snapshotsRootPrefix() string {
	if s.prefix == "" {
		return "snapshots/"
	}
	return s.prefix + "/snapshots/"
}

// Push walks localDir and uploads every file to S3 under
// {prefix}/snapshots/{name}/{relpath}.
func (s *S3SnapshotStore) Push(ctx context.Context, name, localDir string) error {
	keyPrefix := s.snapshotPrefix(name)
	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, localDir)
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
		key := keyPrefix + filepath.ToSlash(rel)

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open %s for upload: %w", path, err)
		}
		defer func() { _ = f.Close() }()

		if _, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
			Body:   f,
		}); err != nil {
			return fmt.Errorf("failed to upload %s to s3://%s/%s: %w", path, s.bucket, key, err)
		}
		return nil
	})
}

// Pull downloads all objects under {prefix}/snapshots/{name}/ to localDir.
func (s *S3SnapshotStore) Pull(ctx context.Context, name, localDir string) error {
	keyPrefix := s.snapshotPrefix(name)

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(keyPrefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list s3://%s/%s: %w", s.bucket, keyPrefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			rel := strings.TrimPrefix(*obj.Key, keyPrefix)
			if rel == "" {
				continue
			}
			localPath := filepath.Join(localDir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
				return fmt.Errorf("failed to create dir for %s: %w", localPath, err)
			}
			f, err := os.Create(localPath)
			if err != nil {
				return fmt.Errorf("failed to create %s: %w", localPath, err)
			}
			_, dlErr := s.downloader.Download(ctx, f, &s3.GetObjectInput{
				Bucket: aws.String(s.bucket),
				Key:    obj.Key,
			})
			_ = f.Close()
			if dlErr != nil {
				return fmt.Errorf("failed to download s3://%s/%s: %w", s.bucket, *obj.Key, dlErr)
			}
		}
	}
	return nil
}

// List returns snapshot names by listing common prefixes under
// {prefix}/snapshots/ with delimiter "/".
func (s *S3SnapshotStore) List(ctx context.Context) ([]string, error) {
	rootPrefix := s.snapshotsRootPrefix()

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucket),
		Prefix:    aws.String(rootPrefix),
		Delimiter: aws.String("/"),
	})

	var names []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list snapshots in s3://%s/%s: %w", s.bucket, rootPrefix, err)
		}
		for _, cp := range page.CommonPrefixes {
			if cp.Prefix == nil {
				continue
			}
			// Strip the root prefix and trailing slash to get the snapshot name.
			name := strings.TrimPrefix(*cp.Prefix, rootPrefix)
			name = strings.TrimSuffix(name, "/")
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// Delete removes all objects under {prefix}/snapshots/{name}/.
func (s *S3SnapshotStore) Delete(ctx context.Context, name string) error {
	keyPrefix := s.snapshotPrefix(name)

	// Collect all keys to delete.
	var objects []types.ObjectIdentifier

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(keyPrefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list objects for deletion s3://%s/%s: %w", s.bucket, keyPrefix, err)
		}
		for _, obj := range page.Contents {
			if obj.Key != nil {
				objects = append(objects, types.ObjectIdentifier{Key: obj.Key})
			}
		}
	}

	if len(objects) == 0 {
		return nil
	}

	// Delete in batches of 1000 (S3 API limit).
	const batchSize = 1000
	for i := 0; i < len(objects); i += batchSize {
		end := i + batchSize
		if end > len(objects) {
			end = len(objects)
		}
		batch := objects[i:end]
		_, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{
				Objects: batch,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return fmt.Errorf("failed to delete objects from s3://%s: %w", s.bucket, err)
		}
	}
	return nil
}
