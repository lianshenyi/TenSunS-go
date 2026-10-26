package alicloud

import (
	"context"
	"errors"
	"strings"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	redis "github.com/alibabacloud-go/r-kvstore-20150101/v5/client"
	rds "github.com/alibabacloud-go/rds-20140815/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

type fakeLoader struct{ err error }

func (f fakeLoader) Load(context.Context, string) (Credentials, error) { return Credentials{}, f.err }

func TestInventoryMappingAndPagination(t *testing.T) {
	ecsCalls, rdsCalls, redisCalls := 0, 0, 0
	p := &Provider{credentials: fakeLoader{}, newClients: func(Credentials, string) (clients, error) {
		return clients{
			ecs: func(request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
				ecsCalls++
				if tea.StringValue(request.RegionId) != "cn-hangzhou" || tea.Int32Value(request.MaxResults) != 100 {
					t.Fatalf("ecs request=%+v", request)
				}
				token := ""
				if ecsCalls == 1 {
					token = "next"
				}
				id := "i-" + string(rune('0'+ecsCalls))
				return &ecs.DescribeInstancesResponse{Body: &ecs.DescribeInstancesResponseBody{NextToken: tea.String(token), Instances: &ecs.DescribeInstancesResponseBodyInstances{Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{{InstanceId: tea.String(id), InstanceName: tea.String("host"), ResourceGroupId: tea.String("rg-1"), OSType: tea.String("linux"), Status: tea.String("Running"), Cpu: tea.Int32(2), Memory: tea.Int32(2048), InnerIpAddress: &ecs.DescribeInstancesResponseBodyInstancesInstanceInnerIpAddress{IpAddress: []*string{tea.String("10.0.0.1")}}, PublicIpAddress: &ecs.DescribeInstancesResponseBodyInstancesInstancePublicIpAddress{IpAddress: []*string{tea.String("1.2.3.4")}}}}}}}, nil
			},
			rds: func(request *rds.DescribeDBInstancesRequest) (*rds.DescribeDBInstancesResponse, error) {
				rdsCalls++
				if tea.StringValue(request.Engine) != "MySQL" || tea.Int32Value(request.MaxResults) != 100 {
					t.Fatalf("rds request=%+v", request)
				}
				token := ""
				if rdsCalls == 1 {
					token = "next"
				}
				id := "rm-" + string(rune('0'+rdsCalls))
				return &rds.DescribeDBInstancesResponse{Body: &rds.DescribeDBInstancesResponseBody{NextToken: tea.String(token), Items: &rds.DescribeDBInstancesResponseBodyItems{DBInstance: []*rds.DescribeDBInstancesResponseBodyItemsDBInstance{{DBInstanceId: tea.String(id), ConnectionString: tea.String("db.example"), ResourceGroupId: tea.String("rg-1"), DBInstanceStatus: tea.String("Running"), DBInstanceType: tea.String("Primary"), EngineVersion: tea.String("8.0")}}}}}, nil
			},
			redis: func(request *redis.DescribeInstancesRequest) (*redis.DescribeInstancesResponse, error) {
				redisCalls++
				if tea.Int32Value(request.PageSize) != 50 {
					t.Fatalf("redis request=%+v", request)
				}
				id := "r-" + string(rune('0'+redisCalls))
				return &redis.DescribeInstancesResponse{Body: &redis.DescribeInstancesResponseBody{TotalCount: tea.Int32(2), Instances: &redis.DescribeInstancesResponseBodyInstances{KVStoreInstance: []*redis.DescribeInstancesResponseBodyInstancesKVStoreInstance{{InstanceId: tea.String(id), InstanceName: tea.String("cache"), ConnectionDomain: tea.String("cache.example"), PrivateIp: tea.String("10.0.0.2"), Port: tea.Int64(6379), ResourceGroupId: tea.String("rg-1"), InstanceStatus: tea.String("Normal"), ArchitectureType: tea.String("standard"), EngineVersion: tea.String("7.0"), Capacity: tea.Int64(1024)}}}}}, nil
			},
		}, nil
	}}
	e, err := p.ECS(context.Background(), "main", "cn-hangzhou", true)
	if err != nil || len(e) != 2 || e["i-1"].IP != "1.2.3.4" || e["i-1"].Group != "rg-1" {
		t.Fatalf("ECS=%+v err=%v", e, err)
	}
	d, err := p.RDS(context.Background(), "main", "cn-hangzhou")
	if err != nil || len(d) != 2 || d["rm-1"].Port != 3306 {
		t.Fatalf("RDS=%+v err=%v", d, err)
	}
	r, err := p.Redis(context.Background(), "main", "cn-hangzhou")
	if err != nil || len(r) != 2 || r["r-1"].Domain != "cache.example" {
		t.Fatalf("Redis=%+v err=%v", r, err)
	}
}

func TestInventoryFailureIsRedactedAndCanceled(t *testing.T) {
	p := &Provider{credentials: fakeLoader{}, newClients: func(Credentials, string) (clients, error) {
		return clients{ecs: func(*ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
			return nil, errors.New("AccessKeySecret=never-show")
		}}, nil
	}}
	_, err := p.ECS(context.Background(), "main", "cn-hangzhou", false)
	if err == nil || !strings.Contains(err.Error(), "DescribeInstances") || strings.Contains(err.Error(), "never-show") {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = p.ECS(ctx, "main", "cn-hangzhou", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
