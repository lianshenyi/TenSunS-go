// Package tencent adapts Tencent Cloud's official SDK to the shared model.
package tencent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	cdb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdb/v20170320"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
	dcdb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/dcdb/v20180411"
	redis "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/redis/v20180412"
)

// Provider creates short-lived Tencent SDK clients so decrypted credentials do
// not remain in memory between scheduler executions.
type Provider struct {
	Secrets  cloud.SecretResolver
	Endpoint string // test-only HTTP endpoint override.
}

func New(secrets cloud.SecretResolver) *Provider { return &Provider{Secrets: secrets} }

func (p *Provider) Group(ctx context.Context, account string) (map[string]string, error) {
	secret, err := p.Secrets.Resolve(ctx, "tencent_cloud", account)
	if err != nil {
		return nil, err
	}
	// DCDB is the only Tencent API that exposes the shared ProjectId catalogue.
	client, _ := dcdb.NewClient(common.NewCredential(secret.AccessKey, secret.SecretKey), "ap-guangzhou", p.profile())
	response, err := client.DescribeProjectsWithContext(ctx, dcdb.NewDescribeProjectsRequest())
	if err != nil {
		return nil, fmt.Errorf("Tencent DCDB DescribeProjects: %w", err)
	}
	groups := make(map[string]string)
	if response.Response == nil {
		return groups, nil
	}
	for _, project := range response.Response.Projects {
		if project.ProjectId != nil && project.Name != nil {
			groups[strconv.FormatInt(*project.ProjectId, 10)] = *project.Name
		}
	}
	return groups, nil
}

func (p *Provider) ECS(ctx context.Context, account, region string, external bool) (map[string]model.ECS, error) {
	secret, err := p.Secrets.Resolve(ctx, "tencent_cloud", account)
	if err != nil {
		return nil, err
	}
	client, _ := cvm.NewClient(common.NewCredential(secret.AccessKey, secret.SecretKey), region, p.profile())
	out := map[string]model.ECS{}
	for offset := int64(0); ; offset += 100 {
		request := cvm.NewDescribeInstancesRequest()
		request.Offset, request.Limit = &offset, int64p(100)
		resp, err := client.DescribeInstancesWithContext(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("Tencent CVM DescribeInstances: %w", err)
		}
		if resp.Response == nil {
			break
		}
		for _, v := range resp.Response.InstanceSet {
			if v.InstanceId == nil {
				continue
			}
			ip := first(v.PrivateIpAddresses)
			if external {
				ip = first(v.PublicIpAddresses)
			}
			tags := map[string]string{}
			for _, t := range v.Tags {
				if t.Key != nil && t.Value != nil {
					tags[*t.Key] = *t.Value
				}
			}
			exp := "按量"
			if v.ExpiredTime != nil && *v.ExpiredTime != "" {
				exp = (*v.ExpiredTime)[:min(10, len(*v.ExpiredTime))]
			}
			group := "0"
			if v.Placement != nil {
				group = strconv.FormatInt(num(v.Placement.ProjectId), 10)
			}
			out[*v.InstanceId] = model.ECS{Name: str(v.InstanceName), Group: group, OSType: os(str(v.OsName)), Status: str(v.InstanceState), Region: region, IP: ip, CPU: fmt.Sprintf("%d核", num(v.CPU)), Mem: fmt.Sprintf("%dGB", num(v.Memory)), Exp: exp, Tags: tags}
		}
		if len(resp.Response.InstanceSet) < 100 {
			break
		}
	}
	return out, nil
}

func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	secret, err := p.Secrets.Resolve(ctx, "tencent_cloud", account)
	if err != nil {
		return nil, err
	}
	client, _ := cdb.NewClient(common.NewCredential(secret.AccessKey, secret.SecretKey), region, p.profile())
	request := cdb.NewDescribeDBInstancesRequest()
	request.Limit = uint64p(2000)
	resp, err := client.DescribeDBInstancesWithContext(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("Tencent CDB DescribeDBInstances: %w", err)
	}
	out := map[string]model.RDS{}
	for _, v := range resp.Response.Items {
		if v.InstanceId == nil {
			continue
		}
		ip := str(v.Vip)
		out[*v.InstanceId] = model.RDS{Name: str(v.InstanceName), Domain: ip, IP: ip, Port: int(num(v.Vport)), Region: region, Group: strconv.FormatInt(num(v.ProjectId), 10), Status: cdbStatus(num(v.Status)), IType: cdbType(num(v.InstanceType)), Ver: str(v.EngineVersion), Exp: deadline(v.DeadlineTime), CPU: "无", Mem: "无", Disk: "无"}
	}
	return out, nil
}

func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	secret, err := p.Secrets.Resolve(ctx, "tencent_cloud", account)
	if err != nil {
		return nil, err
	}
	client, _ := redis.NewClient(common.NewCredential(secret.AccessKey, secret.SecretKey), region, p.profile())
	request := redis.NewDescribeInstancesRequest()
	request.Limit = uint64p(1000)
	resp, err := client.DescribeInstancesWithContext(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("Tencent Redis DescribeInstances: %w", err)
	}
	out := map[string]model.Redis{}
	for _, v := range resp.Response.InstanceSet {
		if v.InstanceId == nil {
			continue
		}
		ip := str(v.WanIp)
		out[*v.InstanceId] = model.Redis{Name: str(v.InstanceName), Domain: ip, IP: ip, Port: int(num(v.Port)), Region: region, Group: strconv.FormatInt(num(v.ProjectId), 10), Status: redisStatus(num(v.Status)), IType: redisType(num(v.Type)), Ver: str(v.Engine), Mem: fmt.Sprintf("%.0fMB", number(v.Size)), Exp: deadline(v.DeadlineTime)}
	}
	return out, nil
}

func first(v []*string) string {
	if len(v) > 0 {
		return str(v[0])
	}
	return ""
}
func str(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func num(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
func number(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
func int64p(v int64) *int64    { return &v }
func uint64p(v uint64) *uint64 { return &v }
func deadline(v *string) string {
	if value := str(v); value != "" && value != "0000-00-00 00:00:00" {
		return value
	}
	return "按量"
}
func cdbStatus(v int64) string {
	if v == 1 {
		return "运行中"
	}
	return "非运行中"
}
func redisStatus(v int64) string {
	if v == 2 {
		return "运行中"
	}
	return "非运行中"
}
func cdbType(v int64) string {
	return map[int64]string{1: "主实例", 2: "灾备实例", 3: "只读实例"}[v]
}
func redisType(v int64) string {
	if v == 5 {
		return "单机"
	}
	if v == 4 || v == 7 || v == 9 || v == 16 || v == 18 || v == 20 || v == 22 || v == 200 {
		return "集群"
	}
	return "主从"
}
func (p *Provider) profile() *profile.ClientProfile {
	result := profile.NewClientProfile()
	if p.Endpoint != "" {
		result.HttpProfile.Scheme = "HTTP"
		result.HttpProfile.Endpoint = strings.TrimPrefix(p.Endpoint, "http://")
	}
	return result
}
func os(v string) string {
	if strings.Contains(strings.ToLower(v), "win") {
		return "windows"
	}
	return "linux"
}

var _ cloud.Provider = (*Provider)(nil)
