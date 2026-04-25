package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func TestListSharesSharedWithUserIntegration(t *testing.T) {
	databaseURL := os.Getenv("VAULT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set VAULT_TEST_DATABASE_URL to run Postgres integration tests")
	}

	schema := fmt.Sprintf("vault_test_%d", time.Now().UnixNano())
	adminDB, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()

	if _, err := adminDB.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	defer func() {
		_, _ = adminDB.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
	}()

	database, err := New(databaseURLWithSearchPath(t, databaseURL, schema))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}

	const (
		ownerUser  = "@owner:example.test"
		viewerUser = "@viewer:example.test"
		roomOne    = "!room-one:example.test"
		roomTwo    = "!room-two:example.test"
		otherRoom  = "!other-room:example.test"
	)
	if _, err := database.GetOrCreateUser(ownerUser, "owner-bucket"); err != nil {
		t.Fatalf("seed owner user: %v", err)
	}
	if _, err := database.GetOrCreateUser(viewerUser, "viewer-bucket"); err != nil {
		t.Fatalf("seed viewer user: %v", err)
	}

	now := time.Now().UTC()
	insertShare(t, database, testShare{
		ownerUserID: ownerUser,
		fileName:    "older-visible.txt",
		shareType:   "room",
		targetID:    roomOne,
		createdAt:   now.Add(-2 * time.Minute),
	})
	insertShare(t, database, testShare{
		ownerUserID: ownerUser,
		fileName:    "newer-visible.txt",
		shareType:   "room",
		targetID:    roomTwo,
		createdAt:   now.Add(-1 * time.Minute),
	})
	insertShare(t, database, testShare{
		ownerUserID: viewerUser,
		fileName:    "self-owned.txt",
		shareType:   "room",
		targetID:    roomOne,
		createdAt:   now,
	})
	insertShare(t, database, testShare{
		ownerUserID: ownerUser,
		fileName:    "public-link.txt",
		shareType:   "link",
		targetID:    roomOne,
		createdAt:   now,
	})
	insertShare(t, database, testShare{
		ownerUserID: ownerUser,
		fileName:    "revoked.txt",
		shareType:   "room",
		targetID:    roomOne,
		isRevoked:   true,
		createdAt:   now,
	})
	insertShare(t, database, testShare{
		ownerUserID: ownerUser,
		fileName:    "expired.txt",
		shareType:   "room",
		targetID:    roomOne,
		expiresAt:   ptr(now.Add(-time.Minute)),
		createdAt:   now,
	})
	insertShare(t, database, testShare{
		ownerUserID: ownerUser,
		fileName:    "other-room.txt",
		shareType:   "room",
		targetID:    otherRoom,
		createdAt:   now,
	})

	shares, err := database.ListSharesSharedWithUser([]string{roomOne, roomTwo}, viewerUser)
	if err != nil {
		t.Fatalf("ListSharesSharedWithUser returned error: %v", err)
	}
	if len(shares) != 2 {
		t.Fatalf("ListSharesSharedWithUser returned %d shares, want 2", len(shares))
	}
	if shares[0].FileName != "newer-visible.txt" || shares[1].FileName != "older-visible.txt" {
		t.Fatalf("shares returned in order %#v, want newest visible shares only", []string{shares[0].FileName, shares[1].FileName})
	}
	if shares[0].OwnerUserID == viewerUser || shares[1].OwnerUserID == viewerUser {
		t.Fatal("ListSharesSharedWithUser returned a self-owned share")
	}

	emptyShares, err := database.ListSharesSharedWithUser(nil, viewerUser)
	if err != nil {
		t.Fatalf("ListSharesSharedWithUser with no targets returned error: %v", err)
	}
	if len(emptyShares) != 0 {
		t.Fatalf("ListSharesSharedWithUser with no targets returned %d shares, want 0", len(emptyShares))
	}
}

func TestUpdateUserTierIntegration(t *testing.T) {
	databaseURL := os.Getenv("VAULT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set VAULT_TEST_DATABASE_URL to run Postgres integration tests")
	}

	schema := fmt.Sprintf("vault_test_%d", time.Now().UnixNano())
	adminDB, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatalf("open admin database: %v", err)
	}
	defer adminDB.Close()

	if _, err := adminDB.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	defer func() {
		_, _ = adminDB.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
	}()

	database, err := New(databaseURLWithSearchPath(t, databaseURL, schema))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}

	const matrixUserID = "@customer:example.test"
	created, err := database.GetOrCreateUser(matrixUserID, "customer-bucket")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if created.Tier != TierFree || created.QuotaBytes != FreeQuotaBytes {
		t.Fatalf("new user tier=%s quota=%d, want free quota=%d", created.Tier, created.QuotaBytes, FreeQuotaBytes)
	}

	updated, err := database.UpdateUserTier(matrixUserID, TierPlus)
	if err != nil {
		t.Fatalf("update to plus: %v", err)
	}
	if updated == nil {
		t.Fatal("update to plus returned nil user")
	}
	if updated.Tier != TierPlus || updated.QuotaBytes != PlusQuotaBytes {
		t.Fatalf("updated tier=%s quota=%d, want plus quota=%d", updated.Tier, updated.QuotaBytes, PlusQuotaBytes)
	}

	stored, err := database.GetUser(matrixUserID)
	if err != nil {
		t.Fatalf("get updated user: %v", err)
	}
	if stored.Tier != TierPlus || stored.QuotaBytes != PlusQuotaBytes {
		t.Fatalf("stored tier=%s quota=%d, want plus quota=%d", stored.Tier, stored.QuotaBytes, PlusQuotaBytes)
	}

	downgraded, err := database.UpdateUserTier(matrixUserID, " Free ")
	if err != nil {
		t.Fatalf("downgrade to free: %v", err)
	}
	if downgraded.Tier != TierFree || downgraded.QuotaBytes != FreeQuotaBytes {
		t.Fatalf("downgraded tier=%s quota=%d, want free quota=%d", downgraded.Tier, downgraded.QuotaBytes, FreeQuotaBytes)
	}

	missing, err := database.UpdateUserTier("@missing:example.test", TierPlus)
	if err != nil {
		t.Fatalf("update missing user: %v", err)
	}
	if missing != nil {
		t.Fatalf("update missing user returned %#v, want nil", missing)
	}
}

type testShare struct {
	ownerUserID string
	fileName    string
	shareType   string
	targetID    string
	expiresAt   *time.Time
	isRevoked   bool
	createdAt   time.Time
}

func insertShare(t *testing.T, database *Database, share testShare) {
	t.Helper()

	_, err := database.db.Exec(
		`INSERT INTO vault_shares
		 (share_id, owner_user_id, object_key, file_name, file_size, mime_type,
		  share_type, target_id, expires_at, is_revoked, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		uuid.New().String(),
		share.ownerUserID,
		"/docs/"+share.fileName,
		share.fileName,
		int64(128),
		"text/plain",
		share.shareType,
		share.targetID,
		share.expiresAt,
		share.isRevoked,
		share.createdAt,
	)
	if err != nil {
		t.Fatalf("insert share %s: %v", share.fileName, err)
	}
}

func databaseURLWithSearchPath(t *testing.T, rawURL, schema string) string {
	t.Helper()

	parsedURL, err := url.Parse(rawURL)
	if err == nil && parsedURL.Scheme != "" {
		query := parsedURL.Query()
		query.Set("options", "-c search_path="+schema)
		parsedURL.RawQuery = query.Encode()
		return parsedURL.String()
	}
	return rawURL + " options='-c search_path=" + schema + "'"
}

func ptr[T any](value T) *T {
	return &value
}
