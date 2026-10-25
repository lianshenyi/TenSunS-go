// Package tencent implements Tencent Cloud's read-only resource providers.
package tencent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	cdb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdb/v20170320"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	cvm "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cvm/v20170312"
	dcdb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/dcdb/v20180411"
	redis "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/redis/v20180412"

	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

const credentialKeyPrefix = "ConsulManager/assets/tencent_cloud/aksk/"

type credentials struct{ accessKey, secretKey string }

type clients struct {
	projects func() (*dcdb.DescribeProjectsResponse, error)
	ecs      func(region string, offset int64) (*cvm.DescribeInstancesResponse, error)
	rds      func(region string) (*cdb.DescribeDBInstancesResponse, error)
	redis    func(region string) (*redis.DescribeInstancesResponse, error)
}

// Provider lists Tencent Cloud resources without invoking write APIs.
type Provider struct {
	load       func(context.Context, string) (credentials, error)
	newClients func(credentials) clients
}

// New builds a Provider that decrypts credentials only in memory before SDK calls.
func New(st store.Store, cipher *cryptox.Cipher) *Provider {
	return &Provider{load: credentialLoader{store: st, cipher: cipher}.load, newClients: sdkClients}
}

// Group lists Tencent Cloud projects through DCDB.
func (p *Provider) Group(ctx context.Context, account string) (map[string]string, error) {
	cs, err := p.clients(ctx, account)
	if err != nil {
		return nil, err
	}
	response, err := cs.projects()
	if err != nil {
		return nil, apiError{"DescribeProjects"}
	}
	groups := map[string]string{}
	if response == nil || response.Response == nil {
		return groups, nil
	}
	for _, project := range response.Response.Projects {
		if project == nil || project.ProjectId == nil {
			continue
		}
		groups[strconv.FormatInt(*project.ProjectId, 10)] = stringValue(project.Name)
	}
	return groups, nil
}

// ECS lists Tencent Cloud VM instances through CVM.
func (p *Provider) ECS(ctx context.Context, account, region string, externalIP bool) (map[string]model.ECS, error) {
	cs, err := p.clients(ctx, account)
	if err != nil {
		return nil, err
	}
	resources := map[string]model.ECS{}
	for offset := int64(0); ; offset += 100 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := cs.ecs(region, offset)
		if err != nil {
			return nil, apiError{"DescribeInstances"}
		}
		if response == nil || response.Response == nil {
			break
		}
		for _, item := range response.Response.InstanceSet {
			if item == nil || item.InstanceId == nil {
				continue
			}
			ip := first(item.PrivateIpAddresses)
			if externalIP && first(item.PublicIpAddresses) != "" {
				ip = first(item.PublicIpAddresses)
			}
			group := "无"
			if item.Placement != nil && item.Placement.ProjectId != nil {
				group = strconv.FormatInt(*item.Placement.ProjectId, 10)
			}
			resources[*item.InstanceId] = model.ECS{Name: stringValue(item.InstanceName), Group: group, OSType: osType(stringValue(item.OsName)), Status: stringValue(item.InstanceState), Region: region, IP: ip, CPU: strconv.FormatInt(int64Value(item.CPU), 10) + "核", Mem: strconv.FormatInt(int64Value(item.Memory), 10) + "GB", Exp: dateOrMetered(stringValue(item.ExpiredTime)), Tags: tags(item.Tags)}
		}
		if response.Response.TotalCount == nil || offset+100 >= *response.Response.TotalCount {
			break
		}
	}
	return resources, nil
}

// RDS lists Tencent Cloud MySQL instances through CDB.
func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	cs, err := p.clients(ctx, account)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := cs.rds(region)
	if err != nil {
		return nil, apiError{"DescribeDBInstances"}
	}
	resources := map[string]model.RDS{}
	if response == nil || response.Response == nil {
		return resources, nil
	}
	for _, item := range response.Response.Items {
		if item != nil && item.InstanceId != nil {
			status := "非运行中"
			if int64Value(item.Status) == 1 {
				status = "运行中"
			}
			resources[*item.InstanceId] = model.RDS{Name: stringValue(item.InstanceName), Domain: stringValue(item.Vip), IP: stringValue(item.Vip), Port: int(int64Value(item.Vport)), Region: region, Group: strconv.FormatInt(int64Value(item.ProjectId), 10), Status: status, IType: rdsType(int64Value(item.InstanceType)), Ver: stringValue(item.EngineVersion), Exp: dateOrMetered(stringValue(item.DeadlineTime)), CPU: strconv.FormatInt(int64Value(item.Cpu), 10) + "核", Mem: strconv.FormatInt(int64Value(item.Memory)/1024, 10) + "GB", Disk: strconv.FormatInt(int64Value(item.Volume), 10) + "GB"}
		}
	}
	return resources, nil
}

// Redis lists Tencent Cloud cache instances through Redis.
func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	cs, err := p.clients(ctx, account)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := cs.redis(region)
	if err != nil {
		return nil, apiError{"DescribeInstances"}
	}
	resources := map[string]model.Redis{}
	if response == nil || response.Response == nil {
		return resources, nil
	}
	for _, item := range response.Response.InstanceSet {
		if item != nil && item.InstanceId != nil {
			status := "非运行中"
			if int64Value(item.Status) == 2 {
				status = "运行中"
			}
			ip := stringValue(item.WanIp)
			resources[*item.InstanceId] = model.Redis{Name: stringValue(item.InstanceName), Domain: ip, IP: ip, Port: int(int64Value(item.Port)), Region: region, Group: strconv.FormatInt(int64Value(item.ProjectId), 10), Status: status, IType: redisType(int64Value(item.Type)), Ver: stringValue(item.CurrentRedisVersion), Mem: strconv.FormatFloat(float64Value(item.Size), 'f', -1, 64) + "MB", Exp: dateOrMetered(stringValue(item.DeadlineTime))}
		}
	}
	return resources, nil
}

func (p *Provider) clients(ctx context.Context, account string) (clients, error) {
	c, err := p.load(ctx, account)
	if err != nil {
		return clients{}, fmt.Errorf("load Tencent Cloud credentials: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return clients{}, err
	}
	return p.newClients(c), nil
}

type apiError struct{ operation string }

func (e apiError) Error() string { return "Tencent Cloud " + e.operation + " request failed" }

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

func sdkClients(c credentials) clients {
	credential := common.NewCredential(c.accessKey, c.secretKey)
	return clients{
		projects: func() (*dcdb.DescribeProjectsResponse, error) {
			client, err := dcdb.NewClient(credential, "ap-guangzhou", profile.NewClientProfile())
			if err != nil {
				return nil, err
			}
			return client.DescribeProjects(dcdb.NewDescribeProjectsRequest())
		},
		ecs: func(region string, offset int64) (*cvm.DescribeInstancesResponse, error) {
			client, err := cvm.NewClient(credential, region, profile.NewClientProfile())
			if err != nil {
				return nil, err
			}
			request := cvm.NewDescribeInstancesRequest()
			request.Offset, request.Limit = &offset, int64Pointer(100)
			return client.DescribeInstances(request)
		},
		rds: func(region string) (*cdb.DescribeDBInstancesResponse, error) {
			client, err := cdb.NewClient(credential, region, profile.NewClientProfile())
			if err != nil {
				return nil, err
			}
			request := cdb.NewDescribeDBInstancesRequest()
			request.Limit = uint64Pointer(2000)
			return client.DescribeDBInstances(request)
		},
		redis: func(region string) (*redis.DescribeInstancesResponse, error) {
			client, err := redis.NewClient(credential, region, profile.NewClientProfile())
			if err != nil {
				return nil, err
			}
			request := redis.NewDescribeInstancesRequest()
			request.Limit = uint64Pointer(1000)
			return client.DescribeInstances(request)
		},
	}
}
func int64Pointer(v int64) *int64    { return &v }
func uint64Pointer(v uint64) *uint64 { return &v }
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func int64Value(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
func float64Value(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
func first(values []*string) string {
	if len(values) == 0 {
		return ""
	}
	return stringValue(values[0])
}
func osType(value string) string {
	if strings.Contains(strings.ToLower(value), "win") {
		return "windows"
	}
	return "linux"
}
func dateOrMetered(value string) string {
	if value == "" || value == "0000-00-00 00:00:00" {
		return "按量"
	}
	if index := strings.IndexByte(value, 'T'); index >= 0 {
		return value[:index]
	}
	if index := strings.IndexByte(value, ' '); index >= 0 {
		return value[:index]
	}
	return value
}
func tags(values []*cvm.Tag) map[string]string {
	result := map[string]string{}
	for _, tag := range values {
		if tag != nil {
			result[stringValue(tag.Key)] = stringValue(tag.Value)
		}
	}
	return result
}
func rdsType(value int64) string {
	return map[int64]string{1: "主实例", 2: "灾备实例", 3: "只读实例"}[value]
}
func redisType(value int64) string {
	return map[int64]string{2: "主从", 3: "主从", 4: "集群", 5: "单机", 6: "主从", 7: "集群", 8: "主从", 9: "集群", 15: "主从", 16: "集群", 17: "主从", 18: "集群"}[value]
}
