package scheduler

import (
	"encoding/json"
	"testing"
)

func TestParseFunc(t *testing.T) {
	cases := []struct {
		fn     string
		kind   Kind
		vendor string
		ok     bool
	}{
		{"__main__:alicloud.group", KindGroup, "alicloud", true},
		{"__main__:tencent_cloud.ecs", KindECS, "tencent_cloud", true},
		{"__main__:huaweicloud.rds", KindRDS, "huaweicloud", true},
		{"__main__:awscloud.redis", KindRedis, "awscloud", true},
		{"__main__:sync_jms.run", KindJMS, "", true},
		{"__main__:avd.notify", "", "", false},
		{"__main__:alicloud.cron", "", "", false},
		{"alicloud.ecs", "", "", false},
	}
	for _, tc := range cases {
		got, err := ParseFunc(tc.fn)
		if tc.ok != (err == nil) {
			t.Errorf("%s: err=%v", tc.fn, err)
			continue
		}
		if !tc.ok {
			continue
		}
		if got.Kind != tc.kind || got.Vendor != tc.vendor {
			t.Errorf("%s: got %+v, want kind=%s vendor=%s", tc.fn, got, tc.kind, tc.vendor)
		}
	}
}

func TestJobSpecArgumentHelpers(t *testing.T) {
	raw := json.RawMessage(`{"id":"alicloud/main/ecs/cn-hangzhou","func":"__main__:alicloud.ecs","args":["main","cn-hangzhou",true],"minutes":10}`)
	spec, err := DecodeJobSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Account() != "main" || spec.Region() != "cn-hangzhou" || !spec.ExternalIP() {
		t.Fatalf("unexpected args: %+v", spec)
	}
	legacy := JobSpec{Args: []any{"main", "cn-hangzhou"}}
	if legacy.ExternalIP() {
		t.Fatal("legacy two-arg ECS job must default isextip=false")
	}
	if (JobSpec{}).Region() != "none" {
		t.Fatal("group region must default to none")
	}
	jms := JobSpec{Args: []any{"alicloud", "main"}}
	if jms.JMSVendor() != "alicloud" || jms.JMSAccount() != "main" {
		t.Fatalf("JMS args = %q/%q", jms.JMSVendor(), jms.JMSAccount())
	}
}

func TestDecodeJobSpecRejectsInvalidMinutes(t *testing.T) {
	if _, err := DecodeJobSpec(json.RawMessage(`{"id":"x","minutes":0}`)); err == nil {
		t.Fatal("expected invalid minutes error")
	}
}
