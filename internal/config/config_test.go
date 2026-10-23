package config

import "testing"

func TestVendorLookup(t *testing.T) {
	if got := VendorDisplay("alicloud"); got != "阿里云" {
		t.Fatalf("VendorDisplay = %q", got)
	}
	internal, ok := InternalVendor("AWS云")
	if !ok || internal != "awscloud" {
		t.Fatalf("InternalVendor = %q, %v", internal, ok)
	}
	if got := VendorDisplay("legacy"); got != "legacy" {
		t.Fatalf("unknown vendor should pass through, got %q", got)
	}
}

func TestRegionDisplay(t *testing.T) {
	cases := []struct{ vendor, region, want string }{
		{"alicloud", "cn-hangzhou", "华东1(杭州)"},
		{"tencent_cloud", "ap-guangzhou", "华南地区(广州)"},
		{"huaweicloud", "cn-north-4", "华北-北京四"},
		{"awscloud", "us-east-1", "美国东部（弗吉尼亚州北部）"},
		{"alicloud", "none", "无"},
		{"alicloud", "unknown", "未找到"},
	}
	for _, tc := range cases {
		if got := RegionDisplay(tc.vendor, tc.region); got != tc.want {
			t.Errorf("RegionDisplay(%q,%q) = %q, want %q", tc.vendor, tc.region, got, tc.want)
		}
	}
}
