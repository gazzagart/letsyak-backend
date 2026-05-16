package db

import "testing"

func TestNormalizeOrgRole(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		wantRole string
		wantErr  bool
	}{
		{name: "owner", role: " Owner ", wantRole: OrgRoleOwner},
		{name: "admin", role: OrgRoleAdmin, wantRole: OrgRoleAdmin},
		{name: "member default", role: "", wantRole: OrgRoleMember},
		{name: "member", role: OrgRoleMember, wantRole: OrgRoleMember},
		{name: "invalid", role: "super-admin", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeOrgRole(tt.role)
			if tt.wantErr {
				if err == nil {
					t.Fatal("NormalizeOrgRole returned nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeOrgRole returned error: %v", err)
			}
			if got != tt.wantRole {
				t.Fatalf("NormalizeOrgRole=%s, want %s", got, tt.wantRole)
			}
		})
	}
}

func TestOrgRolePermissions(t *testing.T) {
	if !CanAssignOrgRole(OrgRoleOwner, OrgRoleOwner) || !CanAssignOrgRole(OrgRoleOwner, OrgRoleAdmin) || !CanAssignOrgRole(OrgRoleOwner, OrgRoleMember) {
		t.Fatal("owner should be able to assign every org role")
	}
	if !CanAssignOrgRole(OrgRoleAdmin, OrgRoleMember) {
		t.Fatal("admin should be able to assign member role")
	}
	if CanAssignOrgRole(OrgRoleAdmin, OrgRoleAdmin) || CanAssignOrgRole(OrgRoleAdmin, OrgRoleOwner) {
		t.Fatal("admin must not be able to assign admin or owner role")
	}
	if !CanManageOrgMember(OrgRoleAdmin, OrgRoleMember) {
		t.Fatal("admin should be able to manage members")
	}
	if CanManageOrgMember(OrgRoleAdmin, OrgRoleOwner) || CanManageOrgMember(OrgRoleAdmin, OrgRoleAdmin) {
		t.Fatal("admin must not be able to manage owners or admins")
	}
}
