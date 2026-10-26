// Package awscloud adapts the official AWS SDK v2 responses to TenSunS's
// stable provider model.
package awscloud

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
)

type ec2API interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}
type rdsAPI interface {
	DescribeDBInstances(context.Context, *rds.DescribeDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error)
}
type cacheAPI interface {
	DescribeCacheClusters(context.Context, *elasticache.DescribeCacheClustersInput, ...func(*elasticache.Options)) (*elasticache.DescribeCacheClustersOutput, error)
}

// Provider uses a resolver to keep long-lived cloud clients from retaining
// plaintext secrets between scheduler runs.
type Provider struct{ Secrets cloud.SecretResolver }

func New(secrets cloud.SecretResolver) *Provider { return &Provider{Secrets: secrets} }

func (p *Provider) Group(context.Context, string) (map[string]string, error) {
	return map[string]string{"1": "1"}, nil
}

func (p *Provider) ECS(ctx context.Context, account, region string, externalIP bool) (map[string]model.ECS, error) {
	ec2client, err := p.clients(ctx, account, region)
	if err != nil {
		return nil, err
	}
	response, err := ec2client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("AWS EC2 DescribeInstances: %w", err)
	}
	out := map[string]model.ECS{}
	for _, reservation := range response.Reservations {
		for _, instance := range reservation.Instances {
			id := aws.ToString(instance.InstanceId)
			if id == "" {
				continue
			}
			name := id
			for _, tag := range instance.Tags {
				if aws.ToString(tag.Key) == "Name" {
					name = aws.ToString(tag.Value)
					break
				}
			}
			ip := aws.ToString(instance.PrivateIpAddress)
			if externalIP {
				ip = aws.ToString(instance.PublicIpAddress)
			}
			cpu := int32(0)
			if instance.CpuOptions != nil {
				cpu = aws.ToInt32(instance.CpuOptions.CoreCount) * aws.ToInt32(instance.CpuOptions.ThreadsPerCore)
			}
			out[id] = model.ECS{Name: name, Group: "无", OSType: osType(aws.ToString(instance.PlatformDetails)), Status: string(instance.State.Name), Region: region, IP: ip, CPU: fmt.Sprintf("%d核", cpu), Mem: "0", Exp: "按量"}
		}
	}
	return out, nil
}

func (p *Provider) RDS(ctx context.Context, account, region string) (map[string]model.RDS, error) {
	_, rdsclient, _, err := p.allClients(ctx, account, region)
	if err != nil {
		return nil, err
	}
	response, err := rdsclient.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("AWS RDS DescribeDBInstances: %w", err)
	}
	out := make(map[string]model.RDS, len(response.DBInstances))
	for _, instance := range response.DBInstances {
		id := aws.ToString(instance.DBInstanceIdentifier)
		if id == "" || instance.Endpoint == nil {
			continue
		}
		domain := aws.ToString(instance.Endpoint.Address)
		out[id] = model.RDS{Name: id, Domain: domain, IP: domain, Port: int(aws.ToInt32(instance.Endpoint.Port)), Region: region, Group: aws.ToString(instance.DBClusterIdentifier), Status: aws.ToString(instance.DBInstanceStatus), IType: aws.ToString(instance.Engine), Ver: aws.ToString(instance.EngineVersion), Exp: "按量", CPU: "0", Mem: "0", Disk: "0"}
	}
	return out, nil
}

func (p *Provider) Redis(ctx context.Context, account, region string) (map[string]model.Redis, error) {
	_, _, cacheclient, err := p.allClients(ctx, account, region)
	if err != nil {
		return nil, err
	}
	response, err := cacheclient.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{ShowCacheNodeInfo: aws.Bool(true)})
	if err != nil {
		return nil, fmt.Errorf("AWS ElastiCache DescribeCacheClusters: %w", err)
	}
	out := make(map[string]model.Redis, len(response.CacheClusters))
	for _, cluster := range response.CacheClusters {
		id := aws.ToString(cluster.CacheClusterId)
		if id == "" {
			continue
		}
		ip := ""
		if len(cluster.CacheNodes) > 0 && cluster.CacheNodes[0].Endpoint != nil {
			ip = aws.ToString(cluster.CacheNodes[0].Endpoint.Address)
		}
		out[id] = model.Redis{Name: id, Domain: ip, IP: ip, Port: 6379, Region: region, Group: aws.ToString(cluster.ReplicationGroupId), Status: aws.ToString(cluster.CacheClusterStatus), IType: aws.ToString(cluster.Engine), Ver: aws.ToString(cluster.EngineVersion), Mem: "0", Exp: "按量"}
	}
	return out, nil
}

func (p *Provider) clients(ctx context.Context, account, region string) (ec2API, error) {
	ec2client, _, _, err := p.allClients(ctx, account, region)
	return ec2client, err
}
func (p *Provider) allClients(ctx context.Context, account, region string) (*ec2.Client, *rds.Client, *elasticache.Client, error) {
	secret, err := p.Secrets.Resolve(ctx, "awscloud", account)
	if err != nil {
		return nil, nil, nil, err
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(secret.AccessKey, secret.SecretKey, "")))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure AWS SDK: %w", err)
	}
	return ec2.NewFromConfig(cfg), rds.NewFromConfig(cfg), elasticache.NewFromConfig(cfg), nil
}
func osType(value string) string {
	if strings.Contains(strings.ToLower(value), "win") {
		return "windows"
	}
	return "linux"
}

var _ cloud.Provider = (*Provider)(nil)
