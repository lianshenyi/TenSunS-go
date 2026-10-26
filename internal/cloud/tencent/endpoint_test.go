package tencent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
)

func TestTencentSDKUsesHTTPEndpointFixture(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TC-Action") != "DescribeInstances" {
			t.Fatalf("X-TC-Action = %q", r.Header.Get("X-TC-Action"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"InstanceSet":[],"RequestId":"x"}}`))
	}))
	defer fixture.Close()
	p := profile.NewClientProfile()
	p.HttpProfile.Scheme = "HTTP"
	p.HttpProfile.Endpoint = strings.TrimPrefix(fixture.URL, "http://")
	client, err := cvm.NewClient(common.NewCredential("ak", "sk"), "ap-guangzhou", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.DescribeInstancesWithContext(context.Background(), cvm.NewDescribeInstancesRequest()); err != nil {
		t.Fatal(err)
	}
}
