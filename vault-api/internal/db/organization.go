package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	OrgRoleOwner  = "owner"
	OrgRoleAdmin  = "admin"
	OrgRoleMember = "member"

	OrgMemberStatusActive  = "active"
	OrgMemberStatusRemoved = "removed"
)

var ErrLastOwner = errors.New("organisation must keep at least one owner")

type Organization struct {
	ID                string
	Name              string
	Slug              string
	OwnerUserID       string
	StoragePlan       string
	SeatLimit         int
	StorageQuotaBytes int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type OrganizationMembership struct {
	Organization
	Role         string
	Status       string
	AssignedTier string
}

type OrganizationMember struct {
	OrgID        string
	MatrixUserID string
	Role         string
	Status       string
	AssignedTier string
	UsedBytes    int64
	QuotaBytes   int64
	VaultTier    string
	CreatedAt    time.Time
	RemovedAt    *time.Time
}

type OrganizationUsage struct {
	OrgID             string
	ActiveMembers     int
	AssignedPlusSeats int
	TotalUsedBytes    int64
	TotalQuotaBytes   int64
	OverQuotaMembers  int
	Members           []*OrganizationMember
}

func NormalizeOrgRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case OrgRoleOwner:
		return OrgRoleOwner, nil
	case OrgRoleAdmin:
		return OrgRoleAdmin, nil
	case "", OrgRoleMember:
		return OrgRoleMember, nil
	default:
		return "", fmt.Errorf("unknown organisation role %q", role)
	}
}

func IsOrgAdminRole(role string) bool {
	return role == OrgRoleOwner || role == OrgRoleAdmin
}

func CanAssignOrgRole(actorRole, newRole string) bool {
	if actorRole == OrgRoleOwner {
		return true
	}
	return actorRole == OrgRoleAdmin && newRole == OrgRoleMember
}

func CanManageOrgMember(actorRole, targetRole string) bool {
	if actorRole == OrgRoleOwner {
		return true
	}
	return actorRole == OrgRoleAdmin && targetRole == OrgRoleMember
}

func (d *Database) CreateOrganization(name, slug, ownerUserID string) (*OrganizationMembership, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("organisation name is required")
	}
	if strings.TrimSpace(slug) == "" {
		return nil, fmt.Errorf("organisation slug is required")
	}

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	org := &OrganizationMembership{Role: OrgRoleOwner, Status: OrgMemberStatusActive, AssignedTier: TierFree}
	err = tx.QueryRow(
		`INSERT INTO organisations (name, slug, owner_user_id)
		 VALUES ($1, $2, $3)
		 RETURNING id, name, slug, owner_user_id, storage_plan, seat_limit, storage_quota_bytes, created_at, updated_at`,
		strings.TrimSpace(name), strings.TrimSpace(slug), ownerUserID,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.OwnerUserID, &org.StoragePlan, &org.SeatLimit, &org.StorageQuotaBytes, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create organisation: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO organisation_members (org_id, matrix_user_id, role, status, assigned_tier)
		 VALUES ($1, $2, $3, $4, $5)`,
		org.ID, ownerUserID, OrgRoleOwner, OrgMemberStatusActive, TierFree,
	); err != nil {
		return nil, fmt.Errorf("create owner membership: %w", err)
	}
	if err := insertAuditTx(tx, org.ID, ownerUserID, "organisation.create", ownerUserID, fmt.Sprintf(`{"name":%q}`, org.Name)); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return org, nil
}

func (d *Database) ListOrganizationsForUser(matrixUserID string) ([]*OrganizationMembership, error) {
	rows, err := d.db.Query(
		`SELECT o.id, o.name, o.slug, o.owner_user_id, o.storage_plan, o.seat_limit,
		        o.storage_quota_bytes, o.created_at, o.updated_at,
		        m.role, m.status, m.assigned_tier
		 FROM organisations o
		 JOIN organisation_members m ON m.org_id = o.id
		 WHERE m.matrix_user_id = $1 AND m.status = 'active'
		 ORDER BY o.name ASC`,
		matrixUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orgs := []*OrganizationMembership{}
	for rows.Next() {
		org := &OrganizationMembership{}
		if err := rows.Scan(
			&org.ID, &org.Name, &org.Slug, &org.OwnerUserID, &org.StoragePlan, &org.SeatLimit,
			&org.StorageQuotaBytes, &org.CreatedAt, &org.UpdatedAt,
			&org.Role, &org.Status, &org.AssignedTier,
		); err != nil {
			return nil, err
		}
		orgs = append(orgs, org)
	}
	return orgs, rows.Err()
}

func (d *Database) GetOrganizationRole(orgID, matrixUserID string) (string, bool, error) {
	var role string
	err := d.db.QueryRow(
		`SELECT role FROM organisation_members
		 WHERE org_id = $1 AND matrix_user_id = $2 AND status = 'active'`,
		orgID, matrixUserID,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return role, true, nil
}

func (d *Database) GetOrganizationMember(orgID, matrixUserID string) (*OrganizationMember, error) {
	member := &OrganizationMember{}
	err := scanOrganizationMember(d.db.QueryRow(
		`SELECT m.org_id, m.matrix_user_id, m.role, m.status, m.assigned_tier,
		        u.used_bytes, u.quota_bytes, u.tier, m.created_at, m.removed_at
		 FROM organisation_members m
		 JOIN vault_users u ON u.matrix_user_id = m.matrix_user_id
		 WHERE m.org_id = $1 AND m.matrix_user_id = $2`,
		orgID, matrixUserID,
	), member)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return member, nil
}

func (d *Database) ListOrganizationMembers(orgID string) ([]*OrganizationMember, error) {
	rows, err := d.db.Query(
		`SELECT m.org_id, m.matrix_user_id, m.role, m.status, m.assigned_tier,
		        u.used_bytes, u.quota_bytes, u.tier, m.created_at, m.removed_at
		 FROM organisation_members m
		 JOIN vault_users u ON u.matrix_user_id = m.matrix_user_id
		 WHERE m.org_id = $1 AND m.status = 'active'
		 ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
		          m.matrix_user_id ASC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []*OrganizationMember{}
	for rows.Next() {
		member := &OrganizationMember{}
		if err := scanOrganizationMember(rows, member); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (d *Database) AddOrganizationMember(orgID, actorUserID, matrixUserID, role, assignedTier string) (*OrganizationMember, error) {
	normalizedRole, err := NormalizeOrgRole(role)
	if err != nil {
		return nil, err
	}
	tierInfo, err := TierInfoFor(assignedTier)
	if err != nil {
		return nil, err
	}

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE vault_users SET tier = $2, quota_bytes = $3 WHERE matrix_user_id = $1`,
		matrixUserID, tierInfo.Tier, tierInfo.QuotaBytes,
	); err != nil {
		return nil, fmt.Errorf("update member tier: %w", err)
	}

	var member OrganizationMember
	err = tx.QueryRow(
		`INSERT INTO organisation_members (org_id, matrix_user_id, role, status, assigned_tier, removed_at)
		 VALUES ($1, $2, $3, 'active', $4, NULL)
		 ON CONFLICT (org_id, matrix_user_id) DO UPDATE
		 SET role = EXCLUDED.role,
		     status = 'active',
		     assigned_tier = EXCLUDED.assigned_tier,
		     removed_at = NULL
		 RETURNING org_id, matrix_user_id, role, status, assigned_tier, created_at, removed_at`,
		orgID, matrixUserID, normalizedRole, tierInfo.Tier,
	).Scan(&member.OrgID, &member.MatrixUserID, &member.Role, &member.Status, &member.AssignedTier, &member.CreatedAt, nullableTimeScanner(&member.RemovedAt))
	if err != nil {
		return nil, fmt.Errorf("add organisation member: %w", err)
	}

	if err := tx.QueryRow(
		`SELECT used_bytes, quota_bytes, tier FROM vault_users WHERE matrix_user_id = $1`,
		matrixUserID,
	).Scan(&member.UsedBytes, &member.QuotaBytes, &member.VaultTier); err != nil {
		return nil, err
	}
	if err := insertAuditTx(tx, orgID, actorUserID, "member.add", matrixUserID, fmt.Sprintf(`{"role":%q,"assigned_tier":%q}`, normalizedRole, tierInfo.Tier)); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &member, nil
}

func (d *Database) UpdateOrganizationMemberRole(orgID, actorUserID, matrixUserID, role string) (*OrganizationMember, error) {
	normalizedRole, err := NormalizeOrgRole(role)
	if err != nil {
		return nil, err
	}

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	currentRole, err := getMemberRoleTx(tx, orgID, matrixUserID)
	if err != nil {
		return nil, err
	}
	if currentRole == "" {
		return nil, nil
	}
	if currentRole == OrgRoleOwner && normalizedRole != OrgRoleOwner {
		if err := ensureAnotherOwnerTx(tx, orgID); err != nil {
			return nil, err
		}
	}

	if _, err := tx.Exec(
		`UPDATE organisation_members SET role = $3 WHERE org_id = $1 AND matrix_user_id = $2 AND status = 'active'`,
		orgID, matrixUserID, normalizedRole,
	); err != nil {
		return nil, err
	}
	if err := insertAuditTx(tx, orgID, actorUserID, "member.role.update", matrixUserID, fmt.Sprintf(`{"old_role":%q,"new_role":%q}`, currentRole, normalizedRole)); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetOrganizationMember(orgID, matrixUserID)
}

func (d *Database) UpdateOrganizationMemberTier(orgID, actorUserID, matrixUserID, tier string) (*OrganizationMember, error) {
	tierInfo, err := TierInfoFor(tier)
	if err != nil {
		return nil, err
	}

	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	role, err := getMemberRoleTx(tx, orgID, matrixUserID)
	if err != nil {
		return nil, err
	}
	if role == "" {
		return nil, nil
	}

	if _, err := tx.Exec(
		`UPDATE organisation_members SET assigned_tier = $3 WHERE org_id = $1 AND matrix_user_id = $2 AND status = 'active'`,
		orgID, matrixUserID, tierInfo.Tier,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(
		`UPDATE vault_users SET tier = $2, quota_bytes = $3 WHERE matrix_user_id = $1`,
		matrixUserID, tierInfo.Tier, tierInfo.QuotaBytes,
	); err != nil {
		return nil, err
	}
	if err := insertAuditTx(tx, orgID, actorUserID, "member.tier.update", matrixUserID, fmt.Sprintf(`{"assigned_tier":%q}`, tierInfo.Tier)); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetOrganizationMember(orgID, matrixUserID)
}

func (d *Database) RemoveOrganizationMember(orgID, actorUserID, matrixUserID string) (bool, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	role, err := getMemberRoleTx(tx, orgID, matrixUserID)
	if err != nil {
		return false, err
	}
	if role == "" {
		return false, nil
	}
	if role == OrgRoleOwner {
		if err := ensureAnotherOwnerTx(tx, orgID); err != nil {
			return false, err
		}
	}

	if _, err := tx.Exec(
		`UPDATE organisation_members
		 SET status = 'removed', assigned_tier = $3, removed_at = NOW()
		 WHERE org_id = $1 AND matrix_user_id = $2 AND status = 'active'`,
		orgID, matrixUserID, TierFree,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec(
		`UPDATE vault_users SET tier = $2, quota_bytes = $3 WHERE matrix_user_id = $1`,
		matrixUserID, TierFree, FreeQuotaBytes,
	); err != nil {
		return false, err
	}
	if err := insertAuditTx(tx, orgID, actorUserID, "member.remove", matrixUserID, fmt.Sprintf(`{"old_role":%q}`, role)); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (d *Database) GetOrganizationUsage(orgID string) (*OrganizationUsage, error) {
	members, err := d.ListOrganizationMembers(orgID)
	if err != nil {
		return nil, err
	}
	usage := &OrganizationUsage{OrgID: orgID, Members: members}
	for _, member := range members {
		usage.ActiveMembers++
		if member.AssignedTier == TierPlus {
			usage.AssignedPlusSeats++
		}
		usage.TotalUsedBytes += member.UsedBytes
		usage.TotalQuotaBytes += member.QuotaBytes
		if member.UsedBytes > member.QuotaBytes {
			usage.OverQuotaMembers++
		}
	}
	return usage, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrganizationMember(scanner rowScanner, member *OrganizationMember) error {
	var removedAt sql.NullTime
	if err := scanner.Scan(
		&member.OrgID, &member.MatrixUserID, &member.Role, &member.Status, &member.AssignedTier,
		&member.UsedBytes, &member.QuotaBytes, &member.VaultTier, &member.CreatedAt, &removedAt,
	); err != nil {
		return err
	}
	if removedAt.Valid {
		member.RemovedAt = &removedAt.Time
	}
	return nil
}

func nullableTimeScanner(target **time.Time) any {
	return &nullableTime{target: target}
}

type nullableTime struct {
	target **time.Time
}

func (n *nullableTime) Scan(value any) error {
	var parsed sql.NullTime
	if err := parsed.Scan(value); err != nil {
		return err
	}
	if parsed.Valid {
		*n.target = &parsed.Time
	} else {
		*n.target = nil
	}
	return nil
}

func getMemberRoleTx(tx *sql.Tx, orgID, matrixUserID string) (string, error) {
	var role string
	err := tx.QueryRow(
		`SELECT role FROM organisation_members WHERE org_id = $1 AND matrix_user_id = $2 AND status = 'active'`,
		orgID, matrixUserID,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

func ensureAnotherOwnerTx(tx *sql.Tx, orgID string) error {
	var ownerCount int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM organisation_members WHERE org_id = $1 AND role = 'owner' AND status = 'active'`,
		orgID,
	).Scan(&ownerCount); err != nil {
		return err
	}
	if ownerCount <= 1 {
		return ErrLastOwner
	}
	return nil
}

func insertAuditTx(tx *sql.Tx, orgID, actorUserID, action, targetUserID, metadataJSON string) error {
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := tx.Exec(
		`INSERT INTO organisation_audit_log (org_id, actor_user_id, action, target_user_id, metadata_json)
		 VALUES ($1, $2, $3, $4, $5::jsonb)`,
		orgID, actorUserID, action, targetUserID, metadataJSON,
	)
	return err
}
