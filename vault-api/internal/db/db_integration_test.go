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

func TestOrganizationLifecycleIntegration(t *testing.T) {
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
		adminUser  = "@admin:example.test"
		memberUser = "@member:example.test"
	)
	if _, err := database.GetOrCreateUser(ownerUser, "owner-bucket"); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	if _, err := database.GetOrCreateUser(adminUser, "admin-bucket"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if member, err := database.GetOrCreateUser(memberUser, "member-bucket"); err != nil {
		t.Fatalf("seed member: %v", err)
	} else if err := database.UpdateUsedBytes(member.MatrixUserID, 1234); err != nil {
		t.Fatalf("seed member usage: %v", err)
	}

	org, err := database.CreateOrganization("Acme Team", "acme-team", ownerUser)
	if err != nil {
		t.Fatalf("create organisation: %v", err)
	}
	if org.Role != OrgRoleOwner || org.OwnerUserID != ownerUser {
		t.Fatalf("created org membership=%#v, want owner membership", org)
	}

	if _, err := database.UpdateOrganizationMemberRole(org.ID, ownerUser, ownerUser, OrgRoleMember); err != ErrLastOwner {
		t.Fatalf("demote only owner error=%v, want ErrLastOwner", err)
	}

	admin, err := database.AddOrganizationMember(org.ID, ownerUser, adminUser, OrgRoleAdmin, TierFree)
	if err != nil {
		t.Fatalf("add admin: %v", err)
	}
	if admin.Role != OrgRoleAdmin || admin.AssignedTier != TierFree {
		t.Fatalf("admin member=%#v, want admin/free", admin)
	}
	secondOwner, err := database.UpdateOrganizationMemberRole(org.ID, ownerUser, adminUser, OrgRoleOwner)
	if err != nil {
		t.Fatalf("promote admin to owner: %v", err)
	}
	if secondOwner.Role != OrgRoleOwner {
		t.Fatalf("second owner role=%s, want owner", secondOwner.Role)
	}

	updatedOwner, err := database.UpdateOrganizationMemberRole(org.ID, adminUser, ownerUser, OrgRoleMember)
	if err != nil {
		t.Fatalf("demote owner after adding admin: %v", err)
	}
	if updatedOwner.Role != OrgRoleMember {
		t.Fatalf("updated owner role=%s, want member", updatedOwner.Role)
	}

	member, err := database.AddOrganizationMember(org.ID, adminUser, memberUser, OrgRoleMember, TierPlus)
	if err != nil {
		t.Fatalf("add plus member: %v", err)
	}
	if member.AssignedTier != TierPlus || member.QuotaBytes != PlusQuotaBytes {
		t.Fatalf("member tier=%s quota=%d, want plus/%d", member.AssignedTier, member.QuotaBytes, PlusQuotaBytes)
	}

	usage, err := database.GetOrganizationUsage(org.ID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.ActiveMembers != 3 || usage.AssignedPlusSeats != 1 || usage.TotalUsedBytes != 1234 {
		t.Fatalf("usage=%#v, want 3 active, 1 plus, 1234 used", usage)
	}

	removed, err := database.RemoveOrganizationMember(org.ID, adminUser, memberUser)
	if err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if !removed {
		t.Fatal("remove member returned false, want true")
	}
	storedMember, err := database.GetUser(memberUser)
	if err != nil {
		t.Fatalf("get removed member user: %v", err)
	}
	if storedMember.Tier != TierFree || storedMember.QuotaBytes != FreeQuotaBytes {
		t.Fatalf("removed member tier=%s quota=%d, want free/%d", storedMember.Tier, storedMember.QuotaBytes, FreeQuotaBytes)
	}

	orgs, err := database.ListOrganizationsForUser(memberUser)
	if err != nil {
		t.Fatalf("list removed member orgs: %v", err)
	}
	if len(orgs) != 0 {
		t.Fatalf("removed member still has %d orgs, want 0", len(orgs))
	}

	var auditCount int
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM organisation_audit_log WHERE org_id = $1`, org.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit log: %v", err)
	}
	if auditCount < 5 {
		t.Fatalf("audit log has %d rows, want at least 5", auditCount)
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
