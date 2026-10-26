// Package alicloud implements read-only Alibaba Cloud inventory adapters.
package alicloud

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/client"
	openapiv2 "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v4/client"
	redis "github.com/alibabacloud-go/r-kvstore-20150101/v5/client"
	rds "github.com/alibabacloud-go/rds-20140815/v5/client"
	resource "github.com/alibabacloud-go/resourcemanager-20200331/v2/client"
	"github.com/alibabacloud-go/tea/tea"

	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

const credentialKeyPrefix = "ConsulManager/assets/alicloud/aksk/"

// Credentials holds an account's decrypted credentials only while making an SDK request.
type Credentials struct{ AccessKeyID, AccessKeySecret string }
type CredentialLoader interface {
	Load(context.Context, string) (Credentials, error)
}

type clients struct {
	groups func(*resource.ListResourceGroupsRequest) (*resource.ListResourceGroupsResponse, error)
	ecs    func(*ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error)
	rds    func(*rds.DescribeDBInstancesRequest) (*rds.DescribeDBInstancesResponse, error)
	redis  func(*redis.DescribeInstancesRequest) (*redis.DescribeInstancesResponse, error)
}

// Provider only issues List/Describe inventory calls; it never changes cloud resources.
type Provider struct {
	credentials CredentialLoader
	newClients  func(Credentials, string) (clients, error)
}

func New(st store.Store, cipher *cryptox.Cipher) *Provider {
	return &Provider{credentials: credentialStore{store: st, cipher: cipher}, newClients: sdkClients}
}

func (p *Provider) Group(ctx context.Context, account string) (map[string]string, error) {
	cs, err := p.clients(ctx, account, "")
	if err != nil {
		return nil, err
	}
	response, err := cs.groups(&resource.ListResourceGroupsRequest{PageNumber: tea.Int32(1), PageSize: tea.Int32(100)})
	if err != nil {
		return nil, apiError{"ListResourceGroups"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	groups := map[string]string{}
	if response == nil || response.Body == nil || response.Body.ResourceGroups == nil {
		return groups, nil
	}
	for _, item := range response.Body.ResourceGroups.ResourceGroup {
		if item != nil && tea.StringValue(item.Id) != "" {
			groups[tea.StringValue(item.Id)] = tea.StringValue(item.DisplayName)
		}
	}
	return groups, nil
}

func (p *Provider) ECS(ctx context.Context, account, region string, externalIP bool) (map[string]model.ECS, error) {
	cs, err := p.clients(ctx, account, region)
	if err != nil {
		return nil, err
	}
	out, token := map[string]model.ECS{}, ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := cs.ecs(&ecs.DescribeInstancesRequest{RegionId: tea.String(region), MaxResults: tea.Int32(100), NextToken: tea.String(token)})
		if err != nil {
			return nil, apiError{"DescribeInstances"}
		}
		if response == nil || response.Body == nil {
			break
		}
		if response.Body.Instances != nil {
			for _, item := range response.Body.Instances.Instance {
				if item != nil && tea.StringValue(item.InstanceId) != "" {
					out[tea.StringValue(item.InstanceId)] = normalizedECS(item, region, externalIP)
				}
			}
		}
		token = tea.StringValue(response.Body.NextToken)
		if token == "" {
			break
		}
	}
	return out, nil
}

func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	cs, err := p.clients(ctx, account, region)
	if err != nil {
		return nil, err
	}
	out, token := map[string]model.RDS{}, ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := cs.rds(&rds.DescribeDBInstancesRequest{RegionId: tea.String(region), Engine: tea.String("MySQL"), MaxResults: tea.Int32(100), NextToken: tea.String(token)})
		if err != nil {
			return nil, apiError{"DescribeDBInstances"}
		}
		if response == nil || response.Body == nil {
			break
		}
		if response.Body.Items != nil {
			for _, item := range response.Body.Items.DBInstance {
				if item != nil && tea.StringValue(item.DBInstanceId) != "" {
					id := tea.StringValue(item.DBInstanceId)
					name := tea.StringValue(item.DBInstanceDescription)
					if name == "" {
						name = "未命名" + id
					}
					out[id] = model.RDS{Name: name, Domain: tea.StringValue(item.ConnectionString), IP: tea.StringValue(item.ConnectionString), Port: 3306, Region: region, Group: resourceGroup(tea.StringValue(item.ResourceGroupId)), Status: tea.StringValue(item.DBInstanceStatus), IType: tea.StringValue(item.DBInstanceType), Ver: tea.StringValue(item.EngineVersion), Exp: date(tea.StringValue(item.ExpireTime)), CPU: "无", Mem: "无", Disk: "无"}
				}
			}
		}
		token = tea.StringValue(response.Body.NextToken)
		if token == "" {
			break
		}
	}
	return out, nil
}

func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	cs, err := p.clients(ctx, account, region)
	if err != nil {
		return nil, err
	}
	out := map[string]model.Redis{}
	for page := int32(1); ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := cs.redis(&redis.DescribeInstancesRequest{RegionId: tea.String(region), PageNumber: tea.Int32(page), PageSize: tea.Int32(50)})
		if err != nil {
			return nil, apiError{"DescribeInstances"}
		}
		if response == nil || response.Body == nil || response.Body.Instances == nil {
			break
		}
		for _, item := range response.Body.Instances.KVStoreInstance {
			if item != nil && tea.StringValue(item.InstanceId) != "" {
				id := tea.StringValue(item.InstanceId)
				name := tea.StringValue(item.InstanceName)
				if name == "" {
					name = "未命名" + id
				}
				out[id] = model.Redis{Name: name, Domain: tea.StringValue(item.ConnectionDomain), IP: tea.StringValue(item.PrivateIp), Port: int(tea.Int64Value(item.Port)), Region: region, Group: resourceGroup(tea.StringValue(item.ResourceGroupId)), Status: tea.StringValue(item.InstanceStatus), IType: tea.StringValue(item.ArchitectureType), Ver: tea.StringValue(item.EngineVersion), Mem: strconv.FormatInt(tea.Int64Value(item.Capacity), 10) + "MB", Exp: date(tea.StringValue(item.EndTime))}
			}
		}
		if int32(len(out)) >= tea.Int32Value(response.Body.TotalCount) {
			break
		}
	}
	return out, nil
}

func (p *Provider) clients(ctx context.Context, account, region string) (clients, error) {
	c, err := p.credentials.Load(ctx, account)
	if err != nil {
		return clients{}, fmt.Errorf("load Alibaba Cloud credentials: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return clients{}, err
	}
	return p.newClients(c, region)
}

type apiError struct{ operation string }

func (e apiError) Error() string { return "Alibaba Cloud " + e.operation + " request failed" }

func sdkClients(c Credentials, region string) (clients, error) {
	config := func(endpoint string) *openapi.Config {
		return &openapi.Config{AccessKeyId: tea.String(c.AccessKeyID), AccessKeySecret: tea.String(c.AccessKeySecret), Endpoint: tea.String(endpoint)}
	}
	configV2 := func(endpoint string) *openapiv2.Config {
		return &openapiv2.Config{AccessKeyId: tea.String(c.AccessKeyID), AccessKeySecret: tea.String(c.AccessKeySecret), Endpoint: tea.String(endpoint)}
	}
	gc, err := resource.NewClient(config("resourcemanager.aliyuncs.com"))
	if err != nil {
		return clients{}, err
	}
	ec, err := ecs.NewClient(configV2("ecs." + region + ".aliyuncs.com"))
	if err != nil {
		return clients{}, err
	}
	rc, err := rds.NewClient(configV2("rds." + region + ".aliyuncs.com"))
	if err != nil {
		return clients{}, err
	}
	rdc, err := redis.NewClient(configV2("r-kvstore." + region + ".aliyuncs.com"))
	if err != nil {
		return clients{}, err
	}
	return clients{groups: gc.ListResourceGroups, ecs: ec.DescribeInstances, rds: rc.DescribeDBInstances, redis: rdc.DescribeInstances}, nil
}

func normalizedECS(item *ecs.DescribeInstancesResponseBodyInstancesInstance, region string, external bool) model.ECS {
	ip := firstInner(item.InnerIpAddress)
	if ip == "" && item.NetworkInterfaces != nil && len(item.NetworkInterfaces.NetworkInterface) > 0 {
		ip = tea.StringValue(item.NetworkInterfaces.NetworkInterface[0].PrimaryIpAddress)
	}
	if external {
		if public := firstPublic(item.PublicIpAddress); public != "" {
			ip = public
		} else if item.EipAddress != nil && tea.StringValue(item.EipAddress.IpAddress) != "" {
			ip = tea.StringValue(item.EipAddress.IpAddress)
		}
	}
	tags := map[string]string{}
	if item.Tags != nil {
		for _, tag := range item.Tags.Tag {
			if tag != nil {
				tags[tea.StringValue(tag.TagKey)] = tea.StringValue(tag.TagValue)
			}
		}
	}
	return model.ECS{Name: tea.StringValue(item.InstanceName), Group: resourceGroup(tea.StringValue(item.ResourceGroupId)), OSType: strings.ToLower(tea.StringValue(item.OSType)), Status: tea.StringValue(item.Status), Region: region, IP: ip, CPU: strconv.Itoa(int(tea.Int32Value(item.Cpu))) + "核", Mem: strconv.FormatFloat(float64(tea.Int32Value(item.Memory))/1024, 'f', 1, 64) + "GB", Exp: date(tea.StringValue(item.ExpiredTime)), Tags: tags}
}
func firstInner(value *ecs.DescribeInstancesResponseBodyInstancesInstanceInnerIpAddress) string {
	if value != nil && len(value.IpAddress) > 0 {
		return tea.StringValue(value.IpAddress[0])
	}
	return ""
}
func firstPublic(value *ecs.DescribeInstancesResponseBodyInstancesInstancePublicIpAddress) string {
	if value != nil && len(value.IpAddress) > 0 {
		return tea.StringValue(value.IpAddress[0])
	}
	return ""
}
func resourceGroup(value string) string {
	if value == "" {
		return "无"
	}
	return value
}
func date(value string) string {
	if value == "" {
		return "-"
	}
	if before, _, ok := strings.Cut(value, "T"); ok {
		return before
	}
	return value
}

type credentialStore struct {
	store  store.Store
	cipher *cryptox.Cipher
}

func (s credentialStore) Load(ctx context.Context, account string) (Credentials, error) {
	var encrypted map[string]string
	found, err := s.store.GetValue(ctx, credentialKeyPrefix+account, &encrypted)
	if err != nil {
		return Credentials{}, fmt.Errorf("read encrypted credentials: %w", err)
	}
	if !found || encrypted["ak"] == "" || encrypted["sk"] == "" {
		return Credentials{}, fmt.Errorf("encrypted credentials are missing")
	}
	ak, err := s.cipher.Decrypt(encrypted["ak"])
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt access key: %w", err)
	}
	sk, err := s.cipher.Decrypt(encrypted["sk"])
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt secret key: %w", err)
	}
	return Credentials{ak, sk}, nil
}
