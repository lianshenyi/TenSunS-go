// Package aws implements AWS's read-only resource provider.
package aws

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/rds"

	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

const credentialKeyPrefix = "ConsulManager/assets/awscloud/aksk/"

type credentialsValue struct{ accessKey, secretKey string }

// Provider only uses AWS Describe APIs. The load seam keeps cloud calls out of
// tests and ensures credentials never leave process memory.
type Provider struct {
	load func(context.Context, string) (credentialsValue, error)
}

// New creates an AWS provider backed by encrypted Consul credentials.
func New(st store.Store, cipher *cryptox.Cipher) *Provider {
	return &Provider{load: credentialLoader{store: st, cipher: cipher}.load}
}

// Group returns the legacy default group: AWS has no matching group API.
// Group returns AWS's legacy fixed group without a cloud API request.
func (p *Provider) Group(context.Context, string) (map[string]string, error) {
	return map[string]string{"1": "1"}, nil
}

// ECS lists EC2 instances using a Describe-only call.
func (p *Provider) ECS(ctx context.Context, account, region string, externalIP bool) (map[string]model.ECS, error) {
	cfg, err := p.config(ctx, account, region)
	if err != nil {
		return nil, err
	}
	response, err := ec2.NewFromConfig(cfg).DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	if err != nil {
		return nil, apiError{"DescribeInstances"}
	}
	result := map[string]model.ECS{}
	for _, reservation := range response.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceId == nil {
				continue
			}
			ip := stringValue(instance.PrivateIpAddress)
			if externalIP && instance.PublicIpAddress != nil {
				ip = *instance.PublicIpAddress
			}
			result[*instance.InstanceId] = model.ECS{Name: tagValue(instance.Tags, "Name"), Group: "无", OSType: osType(stringValue(instance.PlatformDetails)), Status: string(instance.State.Name), Region: region, IP: ip, CPU: cpuCount(instance.CpuOptions), Mem: "", Exp: "按量", Tags: tags(instance.Tags)}
		}
	}
	return result, nil
}

// RDS lists MySQL RDS instances using a Describe-only call.
func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	cfg, err := p.config(ctx, account, region)
	if err != nil {
		return nil, err
	}
	response, err := rds.NewFromConfig(cfg).DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
	if err != nil {
		return nil, apiError{"DescribeDBInstances"}
	}
	result := map[string]model.RDS{}
	for _, instance := range response.DBInstances {
		if instance.DBInstanceIdentifier == nil || !strings.EqualFold(stringValue(instance.Engine), "mysql") {
			continue
		}
		host, port := "", 0
		if instance.Endpoint != nil {
			host, port = stringValue(instance.Endpoint.Address), intValue(instance.Endpoint.Port)
		}
		result[*instance.DBInstanceIdentifier] = model.RDS{Name: *instance.DBInstanceIdentifier, Domain: host, IP: host, Port: port, Region: region, Group: stringValue(instance.DBClusterIdentifier), Status: stringValue(instance.DBInstanceStatus), IType: stringValue(instance.Engine), Ver: stringValue(instance.EngineVersion), CPU: "0", Mem: "0", Disk: "0", Exp: "按量"}
	}
	return result, nil
}

// Redis lists ElastiCache clusters using a Describe-only call.
func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	cfg, err := p.config(ctx, account, region)
	if err != nil {
		return nil, err
	}
	response, err := elasticache.NewFromConfig(cfg).DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{ShowCacheNodeInfo: boolPointer(true)})
	if err != nil {
		return nil, apiError{"DescribeCacheClusters"}
	}
	result := map[string]model.Redis{}
	for _, cluster := range response.CacheClusters {
		if cluster.CacheClusterId == nil {
			continue
		}
		host, port := "", 0
		if len(cluster.CacheNodes) > 0 && cluster.CacheNodes[0].Endpoint != nil {
			host, port = stringValue(cluster.CacheNodes[0].Endpoint.Address), intValue(cluster.CacheNodes[0].Endpoint.Port)
		}
		if port == 0 {
			port = 6379
		}
		result[*cluster.CacheClusterId] = model.Redis{Name: *cluster.CacheClusterId, Domain: host, IP: host, Port: port, Region: region, Group: stringValue(cluster.ReplicationGroupId), Status: stringValue(cluster.CacheClusterStatus), IType: stringValue(cluster.CacheNodeType), Ver: stringValue(cluster.Engine), Mem: "0", Exp: "按量"}
	}
	return result, nil
}

func (p *Provider) config(ctx context.Context, account, region string) (aws.Config, error) {
	if err := ctx.Err(); err != nil {
		return aws.Config{}, err
	}
	c, err := p.load(ctx, account)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load AWS credentials: %w", err)
	}
	return awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.accessKey, c.secretKey, "")))
}

type apiError struct{ operation string }

func (e apiError) Error() string { return "AWS " + e.operation + " request failed" }

type credentialLoader struct {
	store  store.Store
	cipher *cryptox.Cipher
}

func (l credentialLoader) load(ctx context.Context, account string) (credentialsValue, error) {
	var encrypted map[string]string
	found, err := l.store.GetValue(ctx, credentialKeyPrefix+account, &encrypted)
	if err != nil {
		return credentialsValue{}, err
	}
	if !found || encrypted["ak"] == "" || encrypted["sk"] == "" {
		return credentialsValue{}, fmt.Errorf("encrypted credentials are missing")
	}
	ak, err := l.cipher.Decrypt(encrypted["ak"])
	if err != nil {
		return credentialsValue{}, fmt.Errorf("decrypt access key: %w", err)
	}
	sk, err := l.cipher.Decrypt(encrypted["sk"])
	if err != nil {
		return credentialsValue{}, fmt.Errorf("decrypt secret key: %w", err)
	}
	return credentialsValue{ak, sk}, nil
}
func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func intValue(v *int32) int {
	if v == nil {
		return 0
	}
	return int(*v)
}
func int32Value(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}
func cpuCount(value *ec2types.CpuOptions) string {
	if value == nil {
		return "0核"
	}
	return strconv.Itoa(int(int32Value(value.CoreCount)*int32Value(value.ThreadsPerCore))) + "核"
}
func boolPointer(v bool) *bool { return &v }
func osType(value string) string {
	if strings.Contains(strings.ToLower(value), "windows") {
		return "windows"
	}
	return "linux"
}
func tags(values []ec2types.Tag) map[string]string {
	result := map[string]string{}
	for _, tag := range values {
		if tag.Key != nil && tag.Value != nil {
			result[*tag.Key] = *tag.Value
		}
	}
	return result
}
func tagValue(values []ec2types.Tag, key string) string { return tags(values)[key] }
