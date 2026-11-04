// Package huawei adapts Huawei Cloud's official v3 SDK to the shared model.
package huawei

import (
	"context"
	"fmt"
	"strings"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/basic"
	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth/global"
	bss "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/bss/v2"
	bssmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/bss/v2/model"
	dcs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2"
	dcsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2/model"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	ecsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
	eps "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eps/v1"
	epsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eps/v1/model"
	rds "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3"
	rdsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3/model"
	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

type Provider struct {
	Secrets cloud.SecretResolver
	load    func(context.Context, string) (credentials, error)
	group   func(credentials) ([]epsmodel.EpDetail, error)
	// Endpoint overrides service endpoints in tests. Production uses Huawei's regional endpoints.
	Endpoint string
}

func New(st store.Store, cipher *cryptox.Cipher) *Provider {
	return &Provider{Secrets: cloud.SecretResolver{Store: st, Cipher: cipher}}
}
func (p *Provider) Group(ctx context.Context, account string) (map[string]string, error) {
	s, err := p.resolve(ctx, account)
	if err != nil {
		return nil, err
	}
	if p.group != nil {
		projects, err := p.group(credentials{s.AccessKey, s.SecretKey})
		if err != nil {
			return nil, apiError{"ListEnterpriseProject"}
		}
		groups := map[string]string{}
		for _, project := range projects {
			groups[project.Id] = project.Name
		}
		return groups, nil
	}
	// EPS is account-global and its documented endpoint is in cn-north-4.
	c := global.NewCredentialsBuilder().WithAk(s.AccessKey).WithSk(s.SecretKey).Build()
	client := eps.NewEpsClient(eps.EpsClientBuilder().WithEndpoint(p.endpoint("https://eps.cn-north-4.myhuaweicloud.com")).WithCredential(c).Build())
	groups := make(map[string]string)
	for offset := int32(0); ; {
		limit := int32(1000)
		response, err := client.ListEnterpriseProject(&epsmodel.ListEnterpriseProjectRequest{Offset: &offset, Limit: &limit})
		if err != nil {
			return nil, apiError{"ListEnterpriseProject"}
		}
		if response.EnterpriseProjects == nil {
			break
		}
		for _, project := range *response.EnterpriseProjects {
			groups[project.Id] = project.Name
		}
		if response.TotalCount == nil || int32(len(groups)) >= *response.TotalCount || len(*response.EnterpriseProjects) == 0 {
			break
		}
		offset += int32(len(*response.EnterpriseProjects))
	}
	return groups, nil
}

func (p *Provider) ECS(ctx context.Context, account, region string, external bool) (map[string]model.ECS, error) {
	c, err := p.credential(ctx, account)
	if err != nil {
		return nil, err
	}
	expires, err := p.expires(ctx, account)
	if err != nil {
		return nil, err
	}
	client := ecs.NewEcsClient(ecs.EcsClientBuilder().WithEndpoint(p.endpoint("https://ecs." + region + ".myhuaweicloud.com")).WithCredential(c).Build())
	out := map[string]model.ECS{}
	var marker *string
	for {
		limit := int32(100)
		resp, err := client.ListServersDetails(&ecsmodel.ListServersDetailsRequest{Limit: &limit, Marker: marker})
		if err != nil {
			return nil, apiError{"ListServersDetails"}
		}
		if resp.Servers == nil {
			break
		}
		for _, v := range *resp.Servers {
			ip := address(v.Addresses, external)
			exp := expires[v.Id]
			if exp == "" {
				exp = "-"
			}
			cpu, mem := "0核", "0GB"
			if v.Flavor != nil {
				cpu, mem = v.Flavor.Vcpus+"核", memory(v.Flavor.Ram)
			}
			out[v.Id] = model.ECS{Name: v.Name, Group: value(v.EnterpriseProjectId), OSType: os(v.Metadata["os_type"]), Status: v.Status, Region: region, IP: ip, CPU: cpu, Mem: mem, Exp: exp}
		}
		if resp.Count == nil || int32(len(out)) >= *resp.Count || len(*resp.Servers) == 0 {
			break
		}
		next := (*resp.Servers)[len(*resp.Servers)-1].Id
		marker = &next
	}
	return out, nil
}

func (p *Provider) expires(ctx context.Context, account string) (map[string]string, error) {
	s, err := p.resolve(ctx, account)
	if err != nil {
		return nil, err
	}
	c := global.NewCredentialsBuilder().WithAk(s.AccessKey).WithSk(s.SecretKey).Build()
	// BSS is account-global and deliberately queried before ECS to preserve the
	// Python implementation's resource-id to expiry-date contract.
	client := bss.NewBssClient(bss.BssClientBuilder().WithEndpoint(p.endpoint("https://bss.cn-north-1.myhuaweicloud.com")).WithCredential(c).Build())
	limit, onlyMain := int32(500), int32(1)
	status := []int32{2}
	response, err := client.ListPayPerUseCustomerResources(&bssmodel.ListPayPerUseCustomerResourcesRequest{Body: &bssmodel.QueryResourcesReq{Limit: &limit, OnlyMainResource: &onlyMain, StatusList: &status}})
	if err != nil {
		return nil, apiError{"ListPayPerUseCustomerResources"}
	}
	expires := make(map[string]string)
	if response.Data != nil {
		for _, resource := range *response.Data {
			if resource.ResourceId != nil && resource.ExpireTime != nil {
				expires[*resource.ResourceId] = date(*resource.ExpireTime)
			}
		}
	}
	return expires, nil
}
func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	c, err := p.credential(ctx, account)
	if err != nil {
		return nil, err
	}
	client := rds.NewRdsClient(rds.RdsClientBuilder().WithEndpoint(p.endpoint("https://rds." + region + ".myhuaweicloud.com")).WithCredential(c).Build())
	out := map[string]model.RDS{}
	for offset := int32(0); ; {
		limit := int32(100)
		resp, err := client.ListInstances(&rdsmodel.ListInstancesRequest{Limit: &limit, Offset: &offset})
		if err != nil {
			return nil, apiError{"ListInstances"}
		}
		if resp.Instances == nil {
			break
		}
		for _, v := range *resp.Instances {
			if v.Datastore == nil || v.Datastore.Type.Value() != "MySQL" || len(v.PrivateIps) == 0 {
				continue
			}
			domain := v.PrivateIps[0]
			if v.PrivateDnsNames != nil && len(*v.PrivateDnsNames) > 0 {
				domain = (*v.PrivateDnsNames)[0]
			}
			exp := "按量"
			if v.ExpirationTime != nil {
				exp = date(*v.ExpirationTime)
			}
			out[v.Id] = model.RDS{Name: v.Name, Domain: domain, IP: v.PrivateIps[0], Port: int(v.Port), Region: region, Group: v.EnterpriseProjectId, Status: v.Status, IType: v.Type, Ver: v.Datastore.Version, Exp: exp, CPU: value(v.Cpu) + "核", Mem: value(v.Mem) + "GB", Disk: "无"}
		}
		if resp.TotalCount == nil || int32(len(out)) >= *resp.TotalCount || len(*resp.Instances) == 0 {
			break
		}
		offset += int32(len(*resp.Instances))
	}
	return out, nil
}
func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	c, err := p.credential(ctx, account)
	if err != nil {
		return nil, err
	}
	client := dcs.NewDcsClient(dcs.DcsClientBuilder().WithEndpoint(p.endpoint("https://dcs." + region + ".myhuaweicloud.com")).WithCredential(c).Build())
	no := "false"
	out := map[string]model.Redis{}
	for offset := int32(0); ; {
		limit := int32(1000)
		resp, err := client.ListInstances(&dcsmodel.ListInstancesRequest{Limit: &limit, Offset: &offset, IncludeFailure: &no, IncludeDelete: &no})
		if err != nil {
			return nil, apiError{"ListInstances"}
		}
		if resp.Instances == nil {
			break
		}
		for _, v := range *resp.Instances {
			id, ip := value(v.InstanceId), value(v.Ip)
			if id == "" {
				continue
			}
			domain := value(v.DomainName)
			if domain == "" {
				domain = ip
			}
			out[id] = model.Redis{Name: value(v.Name), Domain: domain, IP: ip, Port: int(number(v.Port)), Region: region, Group: value(v.EnterpriseProjectId), Status: value(v.Status), IType: value(v.SpecCode), Ver: value(v.EngineVersion), Mem: fmt.Sprintf("%dMB", number(v.MaxMemory)), Exp: "-"}
		}
		if resp.InstanceNum == nil || int32(len(out)) >= *resp.InstanceNum || len(*resp.Instances) == 0 {
			break
		}
		offset += int32(len(*resp.Instances))
	}
	return out, nil
}
func (p *Provider) credential(ctx context.Context, account string) (*basic.Credentials, error) {
	s, err := p.resolve(ctx, account)
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
func memory(v string) string {
	if v == "" {
		return "0GB"
	}
	var megabytes float64
	if _, err := fmt.Sscan(v, &megabytes); err != nil {
		return "0GB"
	}
	return fmt.Sprintf("%gGB", megabytes/1024)
}
func date(v string) string {
	if len(v) >= 10 {
		return v[:10]
	}
	return v
}
func os(v string) string {
	if strings.Contains(strings.ToLower(v), "win") {
		return "windows"
	}
	return "linux"
}

func (p *Provider) endpoint(defaultEndpoint string) string {
	if p.Endpoint != "" {
		return p.Endpoint
	}
	return defaultEndpoint
}

var _ cloud.Provider = (*Provider)(nil)

type credentials struct{ accessKey, secretKey string }
type apiError struct{ operation string }

func (e apiError) Error() string { return "Huawei Cloud " + e.operation + " request failed" }
func (p *Provider) resolve(ctx context.Context, account string) (cloud.Credentials, error) {
	if err := ctx.Err(); err != nil {
		return cloud.Credentials{}, err
	}
	if p.load != nil {
		c, err := p.load(ctx, account)
		return cloud.Credentials{AccessKey: c.accessKey, SecretKey: c.secretKey}, err
	}
	return p.Secrets.Resolve(ctx, "huaweicloud", account)
}
