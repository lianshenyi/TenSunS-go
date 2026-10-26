package huawei

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
)

func TestHuaweiSDKUsesHTTPEndpointFixture(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/{project_id}/cloudservers/detail" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[]}`))
	}))
	defer fixture.Close()
	credential := basic.NewCredentialsBuilder().WithAk("ak").WithSk("sk").Build()
	client := ecs.NewEcsClient(ecs.EcsClientBuilder().WithEndpoint(fixture.URL).WithCredential(credential).Build())
	if _, err := client.ListServersDetails(&model.ListServersDetailsRequest{}); err != nil {
		t.Fatal(err)
	}
}
