package db

import "testing"

func TestTierInfoFor(t *testing.T) {
	tests := []struct {
		name      string
		tier      string
		wantTier  string
		wantLabel string
		wantQuota int64
		wantLimit string
		wantErr   bool
	}{
		{
			name:      "free tier",
			tier:      TierFree,
			wantTier:  TierFree,
			wantLabel: "Free",
			wantQuota: FreeQuotaBytes,
			wantLimit: "500 MB",
		},
		{
			name:      "plus tier",
			tier:      TierPlus,
			wantTier:  TierPlus,
			wantLabel: "Plus",
			wantQuota: PlusQuotaBytes,
			wantLimit: "5 GB",
		},
		{
			name:      "normalizes user input",
			tier:      " Plus ",
			wantTier:  TierPlus,
			wantLabel: "Plus",
			wantQuota: PlusQuotaBytes,
			wantLimit: "5 GB",
		},
		{
			name:    "rejects unknown tier",
			tier:    "enterprise",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TierInfoFor(tt.tier)
			if tt.wantErr {
				if err == nil {
					t.Fatal("TierInfoFor returned nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("TierInfoFor returned error: %v", err)
			}
			if got.Tier != tt.wantTier || got.Label != tt.wantLabel || got.QuotaBytes != tt.wantQuota || got.LimitLabel != tt.wantLimit {
				t.Fatalf("TierInfoFor returned %#v, want tier=%s label=%s quota=%d limit=%s", got, tt.wantTier, tt.wantLabel, tt.wantQuota, tt.wantLimit)
			}
		})
	}
}

func TestUpgradeTierInfoFor(t *testing.T) {
	got, ok := UpgradeTierInfoFor(TierFree)
	if !ok {
		t.Fatal("UpgradeTierInfoFor(free) returned ok=false, want true")
	}
	if got.Tier != TierPlus || got.QuotaBytes != PlusQuotaBytes || got.LimitLabel != "5 GB" {
		t.Fatalf("UpgradeTierInfoFor(free) returned %#v, want plus tier", got)
	}

	if _, ok := UpgradeTierInfoFor(TierPlus); ok {
		t.Fatal("UpgradeTierInfoFor(plus) returned ok=true, want false")
	}
}
