// Package tencent adapts Tencent Cloud's official SDK to the shared model.
package tencent

import (
	"context"
	"fmt"
	"strings"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	cdb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdb/v20170320"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
	redis "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/redis/v20180412"
)

// Provider creates short-lived Tencent SDK clients so decrypted credentials do
// not remain in memory between scheduler executions.
type Provider struct{ Secrets cloud.SecretResolver }

func New(secrets cloud.SecretResolver) *Provider { return &Provider{Secrets: secrets} }

func (p *Provider) Group(context.Context, string) (map[string]string, error) {
	// Project IDs are returned with each resource; ECS/RDS/Redis stay usable if
	// the account lacks the optional DCDB project-list permission.
	return map[string]string{}, nil
}

func (p *Provider) ECS(ctx context.Context, account, region string, external bool) (map[string]model.ECS, error) {
	secret, err := p.Secrets.Resolve(ctx, "tencent_cloud", account)
	if err != nil {
		return nil, err
	}
	client, _ := cvm.NewClient(common.NewCredential(secret.AccessKey, secret.SecretKey), region, profile.NewClientProfile())
	resp, err := client.DescribeInstancesWithContext(ctx, cvm.NewDescribeInstancesRequest())
	if err != nil {
		return nil, fmt.Errorf("Tencent CVM DescribeInstances: %w", err)
	}
	out := map[string]model.ECS{}
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
		out[*v.InstanceId] = model.ECS{Name: str(v.InstanceName), Group: "无", OSType: os(str(v.OsName)), Status: str(v.InstanceState), Region: region, IP: ip, CPU: fmt.Sprintf("%d核", num(v.CPU)), Mem: fmt.Sprintf("%dGB", num(v.Memory)), Exp: exp, Tags: tags}
	}
	return out, nil
}

func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	secret, err := p.Secrets.Resolve(ctx, "tencent_cloud", account)
	if err != nil {
		return nil, err
	}
	client, _ := cdb.NewClient(common.NewCredential(secret.AccessKey, secret.SecretKey), region, profile.NewClientProfile())
	resp, err := client.DescribeDBInstancesWithContext(ctx, cdb.NewDescribeDBInstancesRequest())
	if err != nil {
		return nil, fmt.Errorf("Tencent CDB DescribeDBInstances: %w", err)
	}
	out := map[string]model.RDS{}
	for _, v := range resp.Response.Items {
		if v.InstanceId == nil {
			continue
		}
		ip := str(v.Vip)
		out[*v.InstanceId] = model.RDS{Name: str(v.InstanceName), Domain: ip, IP: ip, Port: int(num(v.Vport)), Region: region, Group: "无", Status: fmt.Sprintf("%d", num(v.Status)), IType: str(v.DeviceType), Ver: str(v.EngineVersion), Exp: "按量", CPU: "无", Mem: "无", Disk: "无"}
	}
	return out, nil
}

func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	secret, err := p.Secrets.Resolve(ctx, "tencent_cloud", account)
	if err != nil {
		return nil, err
	}
	client, _ := redis.NewClient(common.NewCredential(secret.AccessKey, secret.SecretKey), region, profile.NewClientProfile())
	resp, err := client.DescribeInstancesWithContext(ctx, redis.NewDescribeInstancesRequest())
	if err != nil {
		return nil, fmt.Errorf("Tencent Redis DescribeInstances: %w", err)
	}
	out := map[string]model.Redis{}
	for _, v := range resp.Response.InstanceSet {
		if v.InstanceId == nil {
			continue
		}
		ip := str(v.WanIp)
		out[*v.InstanceId] = model.Redis{Name: str(v.InstanceName), Domain: ip, IP: ip, Port: int(num(v.Port)), Region: region, Group: str(v.ProjectName), Status: fmt.Sprintf("%d", num(v.Status)), IType: fmt.Sprintf("%d", num(v.Type)), Ver: str(v.Engine), Mem: fmt.Sprintf("%.0fMB", number(v.Size)), Exp: "按量"}
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
func os(v string) string {
	if strings.Contains(strings.ToLower(v), "win") {
		return "windows"
	}
	return "linux"
}

var _ cloud.Provider = (*Provider)(nil)
