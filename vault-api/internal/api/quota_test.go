package api

import (
	"testing"
	"time"

	"github.com/garethmaybery/letsyak-vault-api/internal/db"
)

func TestQuotaResponseIncludesDisplayMetadata(t *testing.T) {
	user := &db.VaultUser{
		MatrixUserID: "@user:example.test",
		BucketName:   "user-bucket",
		QuotaBytes:   db.FreeQuotaBytes,
		Tier:         db.TierFree,
		CreatedAt:    time.Now(),
	}

	response := quotaResponse(user, 200*1024*1024)

	if response["used_bytes"] != int64(200*1024*1024) {
		t.Fatalf("used_bytes=%#v, want 200 MB", response["used_bytes"])
	}
	if response["total_bytes"] != db.FreeQuotaBytes {
		t.Fatalf("total_bytes=%#v, want free quota", response["total_bytes"])
	}
	if response["remaining_bytes"] != db.FreeQuotaBytes-int64(200*1024*1024) {
		t.Fatalf("remaining_bytes=%#v, want remaining quota", response["remaining_bytes"])
	}
	if response["tier_label"] != "Free" || response["limit_label"] != "500 MB" {
		t.Fatalf("tier metadata=%#v/%#v, want Free/500 MB", response["tier_label"], response["limit_label"])
	}
	if response["upgrade_available"] != true || response["upgrade_tier"] != db.TierPlus || response["upgrade_limit_label"] != "5 GB" {
		t.Fatalf("upgrade metadata missing or wrong: %#v", response)
	}
	if response["is_over_quota"] != false {
		t.Fatalf("is_over_quota=%#v, want false", response["is_over_quota"])
	}
}

func TestQuotaResponseHandlesOverQuotaPlusUser(t *testing.T) {
	user := &db.VaultUser{
		MatrixUserID: "@user:example.test",
		BucketName:   "user-bucket",
		QuotaBytes:   db.PlusQuotaBytes,
		Tier:         db.TierPlus,
		CreatedAt:    time.Now(),
	}

	response := quotaResponse(user, db.PlusQuotaBytes+1)

	if response["remaining_bytes"] != int64(0) {
		t.Fatalf("remaining_bytes=%#v, want 0", response["remaining_bytes"])
	}
	if response["is_over_quota"] != true {
		t.Fatalf("is_over_quota=%#v, want true", response["is_over_quota"])
	}
	if response["upgrade_available"] != false {
		t.Fatalf("upgrade_available=%#v, want false", response["upgrade_available"])
	}
}
