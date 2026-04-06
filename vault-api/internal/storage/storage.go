package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client wraps a MinIO client for vault operations.
type Client struct {
	mc        *minio.Client // for internal operations (bucket CRUD, listing, etc.)
	presigner *minio.Client // for generating presigned URLs — uses the public endpoint
}

// FileInfo represents a file or folder in the vault.
type FileInfo struct {
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	MIMEType     string    `json:"mime_type,omitempty"`
	LastModified time.Time `json:"last_modified"`
	IsFolder     bool      `json:"is_folder"`
}

// New creates a new MinIO storage client.
// publicBaseURL is the externally-reachable base URL for MinIO (e.g.
// "https://vault-files.example.com"). A separate presigner client is created
// with this endpoint so that presigned URLs have signatures matching the public
// Host header the browser sends via the reverse proxy. If empty, the internal
// client is used for both (useful for local development).
func New(endpoint, accessKey, secretKey string, useSSL bool, publicBaseURL string) (*Client, error) {
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	// Default: use the same client for presigning (local dev).
	presigner := mc
	if publicBaseURL != "" {
		u, err := url.Parse(strings.TrimRight(publicBaseURL, "/"))
		if err != nil {
			return nil, fmt.Errorf("parse public base URL: %w", err)
		}
		presigner, err = minio.New(u.Host, &minio.Options{
			Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
			Secure: u.Scheme == "https",
		})
		if err != nil {
			return nil, fmt.Errorf("create presigner client: %w", err)
		}
	}

	return &Client{
		mc:        mc,
		presigner: presigner,
	}, nil
}

// BucketName generates a deterministic bucket name from a Matrix user ID.
func BucketName(matrixUserID string) string {
	hash := sha256.Sum256([]byte(matrixUserID))
	return fmt.Sprintf("vault-%x", hash[:8])
}

// EnsureBucket creates the user's bucket if it doesn't exist.
func (c *Client) EnsureBucket(ctx context.Context, bucketName string) error {
	exists, err := c.mc.BucketExists(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}
	if exists {
		return nil
	}
	return c.mc.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{})
}

// ListObjects lists files and folders at the given prefix.
func (c *Client) ListObjects(ctx context.Context, bucketName, prefix string) ([]FileInfo, error) {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if prefix == "/" {
		prefix = ""
	}

	var files []FileInfo
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	}

	for obj := range c.mc.ListObjects(ctx, bucketName, opts) {
		if obj.Err != nil {
			return nil, obj.Err
		}

		name := strings.TrimPrefix(obj.Key, prefix)
		if name == "" {
			continue
		}

		isFolder := strings.HasSuffix(name, "/")
		if isFolder {
			name = strings.TrimSuffix(name, "/")
		}

		lastMod := obj.LastModified
		if lastMod.IsZero() {
			lastMod = time.Now()
		}

		files = append(files, FileInfo{
			Name:         name,
			Path:         obj.Key,
			Size:         obj.Size,
			MIMEType:     obj.ContentType,
			LastModified: lastMod,
			IsFolder:     isFolder,
		})
	}
	return files, nil
}

// PresignedPutURL returns a presigned URL for uploading an object.
// Uses the presigner client so the signature matches the public Host header.
func (c *Client) PresignedPutURL(ctx context.Context, bucketName, objectKey string, expiry time.Duration) (string, error) {
	u, err := c.presigner.PresignedPutObject(ctx, bucketName, objectKey, expiry)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PresignedGetURL returns a presigned URL for downloading an object.
// Uses the presigner client so the signature matches the public Host header.
func (c *Client) PresignedGetURL(ctx context.Context, bucketName, objectKey string, expiry time.Duration) (string, error) {
	u, err := c.presigner.PresignedGetObject(ctx, bucketName, objectKey, expiry, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// CreateFolder creates a zero-byte object with a trailing slash to represent a folder.
func (c *Client) CreateFolder(ctx context.Context, bucketName, path string) error {
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	_, err := c.mc.PutObject(ctx, bucketName, path, strings.NewReader(""), 0, minio.PutObjectOptions{})
	return err
}

// DeleteObject deletes a single object.
func (c *Client) DeleteObject(ctx context.Context, bucketName, objectKey string) error {
	return c.mc.RemoveObject(ctx, bucketName, objectKey, minio.RemoveObjectOptions{})
}

// DeletePrefix recursively deletes all objects under the given prefix (folder).
func (c *Client) DeletePrefix(ctx context.Context, bucketName, prefix string) error {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	objectsCh := c.mc.ListObjects(ctx, bucketName, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	for obj := range objectsCh {
		if obj.Err != nil {
			return obj.Err
		}
		if err := c.mc.RemoveObject(ctx, bucketName, obj.Key, minio.RemoveObjectOptions{}); err != nil {
			return err
		}
	}
	return nil
}

// MoveObject copies an object to a new key and deletes the original.
func (c *Client) MoveObject(ctx context.Context, bucketName, fromKey, toKey string) error {
	src := minio.CopySrcOptions{Bucket: bucketName, Object: fromKey}
	dst := minio.CopyDestOptions{Bucket: bucketName, Object: toKey}
	if _, err := c.mc.CopyObject(ctx, dst, src); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return c.mc.RemoveObject(ctx, bucketName, fromKey, minio.RemoveObjectOptions{})
}

// GetBucketSize calculates the total size of all objects in a bucket.
func (c *Client) GetBucketSize(ctx context.Context, bucketName string) (int64, error) {
	var total int64
	for obj := range c.mc.ListObjects(ctx, bucketName, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			return 0, obj.Err
		}
		total += obj.Size
	}
	return total, nil
}
