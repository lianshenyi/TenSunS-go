package tencent

import (
	"context"
	"errors"
	"strings"
	"testing"

	dcdb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/dcdb/v20180411"
)

func TestProviderGroupMapsProjectsAndSanitizesSDKFailure(t *testing.T) {
	id, name := int64(7), "platform"
	p := &Provider{
		load: func(context.Context, string) (credentials, error) { return credentials{}, nil },
		newClients: func(credentials) clients {
			return clients{projects: func() (*dcdb.DescribeProjectsResponse, error) {
				return &dcdb.DescribeProjectsResponse{Response: &dcdb.DescribeProjectsResponseParams{Projects: []*dcdb.Project{{ProjectId: &id, Name: &name}}}}, nil
			}}
		},
	}
	groups, err := p.Group(context.Background(), "demo")
	if err != nil || groups["7"] != "platform" {
		t.Fatalf("groups=%v err=%v", groups, err)
	}
	p.newClients = func(credentials) clients {
		return clients{projects: func() (*dcdb.DescribeProjectsResponse, error) { return nil, errors.New("secret=leak") }}
	}
	_, err = p.Group(context.Background(), "demo")
	if err == nil || !strings.Contains(err.Error(), "DescribeProjects") || strings.Contains(err.Error(), "leak") {
		t.Fatalf("error=%v", err)
	}
}

func TestProviderDoesNotCallSDKAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	p := &Provider{load: func(context.Context, string) (credentials, error) { return credentials{}, nil }, newClients: func(credentials) clients { called = true; return clients{} }}
	if _, err := p.ECS(ctx, "demo", "ap-guangzhou", false); err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
