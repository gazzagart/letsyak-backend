package db

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// Database wraps the PostgreSQL connection for vault metadata.
type Database struct {
	db *sql.DB
}

// VaultUser represents a user's vault account.
type VaultUser struct {
	MatrixUserID string
	BucketName   string
	QuotaBytes   int64
	UsedBytes    int64
	Tier         string
	CreatedAt    time.Time
}

// Share represents a file share record.
type Share struct {
	ShareID       string
	OwnerUserID   string
	ObjectKey     string
	FileName      string
	FileSize      int64
	MIMEType      *string
	ShareType     string
	TargetID      *string
	PasswordHash  *string
	ExpiresAt     *time.Time
	MaxDownloads  *int
	DownloadCount int
	IsRevoked     bool
	CreatedAt     time.Time
}

// New creates a new database connection.
func New(databaseURL string) (*Database, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	return &Database{db: db}, nil
}

// Close closes the database connection.
func (d *Database) Close() error {
	return d.db.Close()
}

// Migrate creates the tables if they don't exist.
func (d *Database) Migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS vault_users (
			matrix_user_id   TEXT PRIMARY KEY,
			bucket_name      TEXT NOT NULL UNIQUE,
			quota_bytes      BIGINT NOT NULL DEFAULT 524288000,
			used_bytes       BIGINT NOT NULL DEFAULT 0,
			tier             TEXT NOT NULL DEFAULT 'free',
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS vault_shares (
			share_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			owner_user_id    TEXT NOT NULL REFERENCES vault_users(matrix_user_id),
			object_key       TEXT NOT NULL,
			file_name        TEXT NOT NULL,
			file_size        BIGINT NOT NULL,
			mime_type        TEXT,
			share_type       TEXT NOT NULL,
			target_id        TEXT,
			password_hash    TEXT,
			expires_at       TIMESTAMPTZ,
			max_downloads    INT,
			download_count   INT NOT NULL DEFAULT 0,
			is_revoked       BOOLEAN NOT NULL DEFAULT FALSE,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_shares_target ON vault_shares(target_id) WHERE NOT is_revoked;
		CREATE INDEX IF NOT EXISTS idx_shares_owner ON vault_shares(owner_user_id);
	`)
	return err
}

// GetOrCreateUser returns the user record, creating it if necessary.
func (d *Database) GetOrCreateUser(matrixUserID, bucketName string) (*VaultUser, error) {
	user := &VaultUser{}
	err := d.db.QueryRow(
		`INSERT INTO vault_users (matrix_user_id, bucket_name)
		 VALUES ($1, $2)
		 ON CONFLICT (matrix_user_id) DO UPDATE SET matrix_user_id = vault_users.matrix_user_id
		 RETURNING matrix_user_id, bucket_name, quota_bytes, used_bytes, tier, created_at`,
		matrixUserID, bucketName,
	).Scan(&user.MatrixUserID, &user.BucketName, &user.QuotaBytes, &user.UsedBytes, &user.Tier, &user.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get or create user: %w", err)
	}
	return user, nil
}

// GetUser returns the user record.
func (d *Database) GetUser(matrixUserID string) (*VaultUser, error) {
	user := &VaultUser{}
	err := d.db.QueryRow(
		`SELECT matrix_user_id, bucket_name, quota_bytes, used_bytes, tier, created_at
		 FROM vault_users WHERE matrix_user_id = $1`,
		matrixUserID,
	).Scan(&user.MatrixUserID, &user.BucketName, &user.QuotaBytes, &user.UsedBytes, &user.Tier, &user.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

// UpdateUsedBytes updates the stored used_bytes for a user.
func (d *Database) UpdateUsedBytes(matrixUserID string, usedBytes int64) error {
	_, err := d.db.Exec(
		`UPDATE vault_users SET used_bytes = $1 WHERE matrix_user_id = $2`,
		usedBytes, matrixUserID,
	)
	return err
}

// CreateShare inserts a new share record.
func (d *Database) CreateShare(s *Share) error {
	s.ShareID = uuid.New().String()
	s.CreatedAt = time.Now().UTC()
	_, err := d.db.Exec(
		`INSERT INTO vault_shares
		 (share_id, owner_user_id, object_key, file_name, file_size, mime_type,
		  share_type, target_id, password_hash, expires_at, max_downloads, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		s.ShareID, s.OwnerUserID, s.ObjectKey, s.FileName, s.FileSize, s.MIMEType,
		s.ShareType, s.TargetID, s.PasswordHash, s.ExpiresAt, s.MaxDownloads, s.CreatedAt,
	)
	return err
}

// GetShare returns a share by ID.
func (d *Database) GetShare(shareID string) (*Share, error) {
	s := &Share{}
	err := d.db.QueryRow(
		`SELECT share_id, owner_user_id, object_key, file_name, file_size, mime_type,
		        share_type, target_id, password_hash, expires_at, max_downloads,
		        download_count, is_revoked, created_at
		 FROM vault_shares WHERE share_id = $1`,
		shareID,
	).Scan(
		&s.ShareID, &s.OwnerUserID, &s.ObjectKey, &s.FileName, &s.FileSize, &s.MIMEType,
		&s.ShareType, &s.TargetID, &s.PasswordHash, &s.ExpiresAt, &s.MaxDownloads,
		&s.DownloadCount, &s.IsRevoked, &s.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get share: %w", err)
	}
	return s, nil
}

// IncrementDownloadCount bumps the download counter.
func (d *Database) IncrementDownloadCount(shareID string) error {
	_, err := d.db.Exec(
		`UPDATE vault_shares SET download_count = download_count + 1 WHERE share_id = $1`,
		shareID,
	)
	return err
}

// RevokeShare marks a share as revoked.
func (d *Database) RevokeShare(shareID, ownerUserID string) error {
	result, err := d.db.Exec(
		`UPDATE vault_shares SET is_revoked = TRUE
		 WHERE share_id = $1 AND owner_user_id = $2`,
		shareID, ownerUserID,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("share not found or not owned by user")
	}
	return nil
}

// ListSharesByOwner returns all shares for a user.
func (d *Database) ListSharesByOwner(ownerUserID string) ([]*Share, error) {
	rows, err := d.db.Query(
		`SELECT share_id, owner_user_id, object_key, file_name, file_size, mime_type,
		        share_type, target_id, password_hash, expires_at, max_downloads,
		        download_count, is_revoked, created_at
		 FROM vault_shares
		 WHERE owner_user_id = $1
		 ORDER BY created_at DESC`,
		ownerUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shares []*Share
	for rows.Next() {
		s := &Share{}
		if err := rows.Scan(
			&s.ShareID, &s.OwnerUserID, &s.ObjectKey, &s.FileName, &s.FileSize, &s.MIMEType,
			&s.ShareType, &s.TargetID, &s.PasswordHash, &s.ExpiresAt, &s.MaxDownloads,
			&s.DownloadCount, &s.IsRevoked, &s.CreatedAt,
		); err != nil {
			return nil, err
		}
		shares = append(shares, s)
	}
	return shares, rows.Err()
}
