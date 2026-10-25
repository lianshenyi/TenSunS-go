// Package huawei implements Huawei Cloud's read-only resource provider.
package huawei

import (
	"context"
	"fmt"
	"strings"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/core/auth"
	dcs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2"
	dcsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dcs/v2/model"
	ecs "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2"
	ecsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/ecs/v2/model"
	eps "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eps/v1"
	epsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/eps/v1/model"
	rds "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3"
	rdsmodel "github.com/huaweicloud/huaweicloud-sdk-go-v3/services/rds/v3/model"

	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

const credentialKeyPrefix = "ConsulManager/assets/huaweicloud/aksk/"

type credentials struct{ accessKey, secretKey string }

// Provider uses only Huawei Cloud list APIs. Functions are injected so mapping
// and error handling can be tested without making cloud requests.
type Provider struct {
	load  func(context.Context, string) (credentials, error)
	group func(credentials) ([]epsmodel.EpDetail, error)
	ecs   func(credentials, string) ([]ecsmodel.ServerDetail, error)
	rds   func(credentials, string) ([]rdsmodel.InstanceResponse, error)
	redis func(credentials, string) ([]dcsmodel.InstanceListInfo, error)
}

// New creates a Huawei Cloud provider backed by encrypted Consul credentials.
func New(st store.Store, cipher *cryptox.Cipher) *Provider {
	return &Provider{load: credentialLoader{store: st, cipher: cipher}.load, group: listGroups, ecs: listECS, rds: listRDS, redis: listRedis}
}

// Group lists enterprise projects through EPS.
func (p *Provider) Group(ctx context.Context, account string) (map[string]string, error) {
	c, err := p.credentials(ctx, account)
	if err != nil {
		return nil, err
	}
	items, err := p.group(c)
	if err != nil {
		return nil, apiError{"ListEnterpriseProject"}
	}
	result := make(map[string]string, len(items))
	for _, item := range items {
		if item.Id != "" {
			result[item.Id] = item.Name
		}
	}
	return result, nil
}

// ECS lists servers through ECS.
func (p *Provider) ECS(ctx context.Context, account, region string, externalIP bool) (map[string]model.ECS, error) {
	c, err := p.credentials(ctx, account)
	if err != nil {
		return nil, err
	}
	items, err := p.ecs(c, region)
	if err != nil {
		return nil, apiError{"ListServersDetails"}
	}
	result := make(map[string]model.ECS, len(items))
	for _, item := range items {
		if item.Id == "" {
			continue
		}
		ip := serverIP(item, externalIP)
		group := "无"
		if item.EnterpriseProjectId != nil {
			group = *item.EnterpriseProjectId
		}
		os := "linux"
		if strings.EqualFold(item.Metadata["os_type"], "windows") {
			os = "windows"
		}
		cpu, mem := "", ""
		if item.Flavor != nil {
			cpu = item.Flavor.Vcpus + "核"
			mem = item.Flavor.Ram + "MB"
		}
		result[item.Id] = model.ECS{Name: item.Name, Group: group, OSType: os, Status: item.Status, Region: region, IP: ip, CPU: cpu, Mem: mem, Exp: "按量"}
	}
	return result, nil
}

// RDS lists MySQL instances through RDS.
func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	c, err := p.credentials(ctx, account)
	if err != nil {
		return nil, err
	}
	items, err := p.rds(c, region)
	if err != nil {
		return nil, apiError{"ListInstances"}
	}
	result := make(map[string]model.RDS, len(items))
	for _, item := range items {
		if item.Id == "" || item.Datastore == nil || !strings.EqualFold(item.Datastore.Type.Value(), "MySQL") {
			continue
		}
		ip, domain := first(item.PrivateIps), firstPointer(item.PrivateDnsNames)
		if domain == "" {
			domain = ip
		}
		version := item.Datastore.Version
		exp := "按量"
		if item.ExpirationTime != nil {
			exp = beforeT(*item.ExpirationTime)
		}
		result[item.Id] = model.RDS{Name: item.Name, Domain: domain, IP: ip, Port: int(item.Port), Region: region, Group: item.EnterpriseProjectId, Status: item.Status, IType: item.Type, Ver: version, CPU: pointerValue(item.Cpu) + "核", Mem: pointerValue(item.Mem) + "GB", Exp: exp}
	}
	return result, nil
}

// Redis lists cache instances through DCS.
func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	c, err := p.credentials(ctx, account)
	if err != nil {
		return nil, err
	}
	items, err := p.redis(c, region)
	if err != nil {
		return nil, apiError{"ListInstances"}
	}
	result := make(map[string]model.Redis, len(items))
	for _, item := range items {
		id := pointerValue(item.InstanceId)
		if id == "" {
			continue
		}
		ip, domain := pointerValue(item.Ip), pointerValue(item.DomainName)
		if domain == "" {
			domain = ip
		}
		result[id] = model.Redis{Name: pointerValue(item.Name), Domain: domain, IP: ip, Port: int(pointerInt(item.Port)), Region: region, Group: pointerValue(item.EnterpriseProjectId), Status: pointerValue(item.Status), IType: pointerValue(item.SpecCode), Ver: pointerValue(item.EngineVersion), Mem: fmt.Sprintf("%dMB", pointerInt(item.MaxMemory)), Exp: "按量"}
	}
	return result, nil
}

func (p *Provider) credentials(ctx context.Context, account string) (credentials, error) {
	if err := ctx.Err(); err != nil {
		return credentials{}, err
	}
	c, err := p.load(ctx, account)
	if err != nil {
		return credentials{}, fmt.Errorf("load Huawei Cloud credentials: %w", err)
	}
	return c, ctx.Err()
}

type apiError struct{ operation string }

func (e apiError) Error() string { return "Huawei Cloud " + e.operation + " request failed" }

type credentialLoader struct {
	store  store.Store
	cipher *cryptox.Cipher
}

func (l credentialLoader) load(ctx context.Context, account string) (credentials, error) {
	var encrypted map[string]string
	found, err := l.store.GetValue(ctx, credentialKeyPrefix+account, &encrypted)
	if err != nil {
		return credentials{}, err
	}
	if !found || encrypted["ak"] == "" || encrypted["sk"] == "" {
		return credentials{}, fmt.Errorf("encrypted credentials are missing")
	}
	ak, err := l.cipher.Decrypt(encrypted["ak"])
	if err != nil {
		return credentials{}, fmt.Errorf("decrypt access key: %w", err)
	}
	sk, err := l.cipher.Decrypt(encrypted["sk"])
	if err != nil {
		return credentials{}, fmt.Errorf("decrypt secret key: %w", err)
	}
	return credentials{ak, sk}, nil
}

func listGroups(c credentials) ([]epsmodel.EpDetail, error) {
	client := eps.NewEpsClient(eps.EpsClientBuilder().WithCredential(auth.NewGlobalCredentialsBuilder().WithAk(c.accessKey).WithSk(c.secretKey).Build()).WithEndpoint("https://eps.cn-north-4.myhuaweicloud.com").Build())
	response, err := client.ListEnterpriseProject(&epsmodel.ListEnterpriseProjectRequest{})
	if err != nil || response.EnterpriseProjects == nil {
		return nil, err
	}
	return *response.EnterpriseProjects, nil
}
func listECS(c credentials, region string) ([]ecsmodel.ServerDetail, error) {
	client := ecs.NewEcsClient(ecs.EcsClientBuilder().WithCredential(auth.NewBasicCredentialsBuilder().WithAk(c.accessKey).WithSk(c.secretKey).Build()).WithEndpoint("https://ecs." + region + ".myhuaweicloud.com").Build())
	limit := int32(1000)
	response, err := client.ListServersDetails(&ecsmodel.ListServersDetailsRequest{Limit: &limit})
	if err != nil || response.Servers == nil {
		return nil, err
	}
	return *response.Servers, nil
}
func listRDS(c credentials, region string) ([]rdsmodel.InstanceResponse, error) {
	client := rds.NewRdsClient(rds.RdsClientBuilder().WithCredential(auth.NewBasicCredentialsBuilder().WithAk(c.accessKey).WithSk(c.secretKey).Build()).WithEndpoint("https://rds." + region + ".myhuaweicloud.com").Build())
	limit := int32(100)
	dataType := rdsmodel.GetListInstancesRequestDatastoreTypeEnum().MY_SQL
	response, err := client.ListInstances(&rdsmodel.ListInstancesRequest{Limit: &limit, DatastoreType: &dataType})
	if err != nil || response.Instances == nil {
		return nil, err
	}
	return *response.Instances, nil
}
func listRedis(c credentials, region string) ([]dcsmodel.InstanceListInfo, error) {
	client := dcs.NewDcsClient(dcs.DcsClientBuilder().WithCredential(auth.NewBasicCredentialsBuilder().WithAk(c.accessKey).WithSk(c.secretKey).Build()).WithEndpoint("https://dcs." + region + ".myhuaweicloud.com").Build())
	limit := int32(1000)
	no := "false"
	response, err := client.ListInstances(&dcsmodel.ListInstancesRequest{Limit: &limit, IncludeFailure: &no, IncludeDelete: &no})
	if err != nil || response.Instances == nil {
		return nil, err
	}
	return *response.Instances, nil
}
func serverIP(item ecsmodel.ServerDetail, external bool) string {
	for _, addresses := range item.Addresses {
		for _, address := range addresses {
			if external && strings.Contains(strings.ToLower(address.OSEXTIPStype.Value()), "floating") {
				return address.Addr
			}
			if address.Addr != "" {
				return address.Addr
			}
		}
	}
	return ""
}
func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
func firstPointer(values *[]string) string {
	if values == nil {
		return ""
	}
	return first(*values)
}
func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func pointerInt(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}
func beforeT(value string) string {
	if before, _, ok := strings.Cut(value, "T"); ok {
		return before
	}
	return value
}
