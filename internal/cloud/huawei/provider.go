// Package huawei adapts Huawei Cloud's official v3 SDK to the shared model.
package huawei

import (
	"context"
	"fmt"
	"strings"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	dcs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2"
	dcsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2/model"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	ecsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
	rds "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3"
	rdsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3/model"
	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
)

type Provider struct{ Secrets cloud.SecretResolver }

func New(secrets cloud.SecretResolver) *Provider { return &Provider{Secrets: secrets} }
func (p *Provider) Group(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (p *Provider) ECS(ctx context.Context, account, region string, external bool) (map[string]model.ECS, error) {
	c, err := p.credential(ctx, account)
	if err != nil {
		return nil, err
	}
	client := ecs.NewEcsClient(ecs.EcsClientBuilder().WithEndpoint("https://ecs." + region + ".myhuaweicloud.com").WithCredential(c).Build())
	limit := int32(1000)
	resp, err := client.ListServersDetails(&ecsmodel.ListServersDetailsRequest{Limit: &limit})
	if err != nil {
		return nil, fmt.Errorf("Huawei ECS ListServersDetails: %w", err)
	}
	out := map[string]model.ECS{}
	if resp.Servers == nil {
		return out, nil
	}
	for _, v := range *resp.Servers {
		ip := address(v.Addresses, external)
		out[v.Id] = model.ECS{Name: v.Name, Group: "无", OSType: os(v.Metadata["os_type"]), Status: v.Status, Region: region, IP: ip, CPU: "0核", Mem: "0GB", Exp: "-"}
	}
	return out, nil
}
func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	c, err := p.credential(ctx, account)
	if err != nil {
		return nil, err
	}
	client := rds.NewRdsClient(rds.RdsClientBuilder().WithEndpoint("https://rds." + region + ".myhuaweicloud.com").WithCredential(c).Build())
	limit := int32(100)
	resp, err := client.ListInstances(&rdsmodel.ListInstancesRequest{Limit: &limit})
	if err != nil {
		return nil, fmt.Errorf("Huawei RDS ListInstances: %w", err)
	}
	out := map[string]model.RDS{}
	if resp.Instances == nil {
		return out, nil
	}
	for _, v := range *resp.Instances {
		if v.Datastore == nil || v.Datastore.Type.Value() != "MySQL" || len(v.PrivateIps) == 0 {
			continue
		}
		domain := v.PrivateIps[0]
		if v.PrivateDnsNames != nil && len(*v.PrivateDnsNames) > 0 {
			domain = (*v.PrivateDnsNames)[0]
		}
		out[v.Id] = model.RDS{Name: v.Name, Domain: domain, IP: v.PrivateIps[0], Port: int(v.Port), Region: region, Group: v.EnterpriseProjectId, Status: v.Status, IType: v.Type, Ver: v.Datastore.Version, Exp: "-", CPU: value(v.Cpu), Mem: value(v.Mem), Disk: "无"}
	}
	return out, nil
}
func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	c, err := p.credential(ctx, account)
	if err != nil {
		return nil, err
	}
	client := dcs.NewDcsClient(dcs.DcsClientBuilder().WithEndpoint("https://dcs." + region + ".myhuaweicloud.com").WithCredential(c).Build())
	limit := int32(1000)
	no := "false"
	resp, err := client.ListInstances(&dcsmodel.ListInstancesRequest{Limit: &limit, IncludeFailure: &no, IncludeDelete: &no})
	if err != nil {
		return nil, fmt.Errorf("Huawei DCS ListInstances: %w", err)
	}
	out := map[string]model.Redis{}
	if resp.Instances == nil {
		return out, nil
	}
	for _, v := range *resp.Instances {
		id, ip := value(v.InstanceId), value(v.Ip)
		out[id] = model.Redis{Name: value(v.Name), Domain: value(v.DomainName), IP: ip, Port: int(number(v.Port)), Region: region, Group: value(v.EnterpriseProjectId), Status: value(v.Status), IType: value(v.SpecCode), Ver: value(v.EngineVersion), Mem: fmt.Sprintf("%dMB", number(v.MaxMemory)), Exp: "-"}
	}
	return out, nil
}
func (p *Provider) credential(ctx context.Context, account string) (*basic.Credentials, error) {
	s, err := p.Secrets.Resolve(ctx, "huaweicloud", account)
	if err != nil {
		return nil, err
	}
	return basic.NewCredentialsBuilder().WithAk(s.AccessKey).WithSk(s.SecretKey).Build(), nil
}
func address(values map[string][]ecsmodel.ServerAddress, external bool) string {
	for _, list := range values {
		for _, v := range list {
			if external && v.OSEXTIPStype != nil && v.OSEXTIPStype.Value() == "floating" {
				return v.Addr
			}
			if !external && (v.OSEXTIPStype == nil || v.OSEXTIPStype.Value() != "floating") {
				return v.Addr
			}
		}
	}
	return ""
}
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func number(v *int32) int32 {
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
