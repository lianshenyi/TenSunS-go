// Package demodata builds the disposable Consul demo fixture.
package demodata

import (
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// New builds demo values and registrations. The mock and the legacy seeder
// use different paths for the JumpServer node record.
func New(nodeKey string) (map[string]any, []store.ServiceRegistration, error) {
	const skey = "0123456789abcdef0123456789abcdef"
	seed := map[string]any{
		"ConsulManager/assets/secret/skey": map[string]string{"sk": skey},
		"ConsulManager/assets/alicloud/group/main": map[string]string{
			"rg-default": "默认资源组",
		},
		"ConsulManager/jobs/alicloud/main/group": map[string]any{
			"id": "alicloud/main/group", "func": "__main__:alicloud.group",
			"args": []any{"main"}, "minutes": 60, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/jobs/alicloud/main/ecs/cn-hangzhou": map[string]any{
			"id": "alicloud/main/ecs/cn-hangzhou", "func": "__main__:alicloud.ecs",
			"args": []any{"main", "cn-hangzhou", false}, "minutes": 10, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/jobs/alicloud/main/rds/cn-hangzhou": map[string]any{
			"id": "alicloud/main/rds/cn-hangzhou", "func": "__main__:alicloud.rds",
			"args": []any{"main", "cn-hangzhou"}, "minutes": 20, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/jobs/alicloud/main/redis/cn-hangzhou": map[string]any{
			"id": "alicloud/main/redis/cn-hangzhou", "func": "__main__:alicloud.redis",
			"args": []any{"main", "cn-hangzhou"}, "minutes": 20, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/record/jobs/alicloud/main/group": map[string]any{
			"count": 2, "update": "10.23/14:10", "status": 20000, "msg": "同步资源组成功！总数：2",
		},
		"ConsulManager/record/jobs/alicloud/main/ecs/cn-hangzhou": map[string]any{
			"count": 2, "update": "10.23/14:12", "status": 20000, "on": 1, "off": 1, "msg": "ECS同步成功！总数：2，开机：1，关机：1",
		},
		"ConsulManager/jms/jobs/alicloud/main": map[string]any{
			"id": "alicloud/main/jms", "func": "__main__:sync_jms.run",
			"args": []any{"alicloud", "main"}, "minutes": 3, "trigger": "interval", "replace_existing": true,
		},
		"ConsulManager/record/jms/alicloud/main": map[string]any{
			"count": 2, "update": "1023/14:10", "status": 20000, "msg": "同步资源成功！总数：2",
		},
		nodeKey: map[string]any{
			"node_id": "00000000-0000-0000-0000-000000000001", "interval": 3,
		},
		"ConsulManager/jms/ecs_info": map[string]any{
			"linux":   []any{[]string{"ssh/22"}, "root"},
			"windows": []any{[]string{"rdp/3389"}, "Administrator"},
		},
		"ConsulManager/jms/custom_ecs_info": map[string]any{},
	}
	cipher, err := cryptox.NewCipher(skey)
	if err != nil {
		return nil, nil, err
	}
	accessKeyID, err := cipher.Encrypt("demo-jumpserver-access-key")
	if err != nil {
		return nil, nil, err
	}
	accessKeySecret, err := cipher.Encrypt("demo-jumpserver-access-secret")
	if err != nil {
		return nil, nil, err
	}
	seed["ConsulManager/jms/jms_info"] = map[string]any{
		"url": "https://jumpserver.example.com", "auth_type": "access_key", "access_key_id": accessKeyID, "access_key_secret": accessKeySecret, "ver": "V3",
	}
	services := []store.ServiceRegistration{
		{ID: "i-demo0001", Name: "alicloud_main_ecs", Address: "10.0.0.11", Port: 9100,
			Tags: []string{"ON", "linux", "cn-hangzhou"},
			Meta: map[string]string{
				"iid": "i-demo0001", "name": "prod-web-01", "region": "华东1(杭州)",
				"group": "默认资源组", "instance": "10.0.0.11:9100", "account": "main",
				"vendor": "阿里云", "os": "linux", "cpu": "4核", "mem": "8GB",
				"exp": "按量", "stat": "on",
			},
		},
		{ID: "i-demo0002", Name: "alicloud_main_ecs", Address: "10.0.0.12", Port: 9182,
			Tags: []string{"OFF", "windows", "cn-hangzhou"},
			Meta: map[string]string{
				"iid": "i-demo0002", "name": "prod-win-01", "region": "华东1(杭州)",
				"group": "默认资源组", "instance": "10.0.0.12:9182", "account": "main",
				"vendor": "阿里云", "os": "windows", "cpu": "8核", "mem": "16GB",
				"exp": "按量", "stat": "off",
			},
		},
		{ID: "rm-demo0001", Name: "alicloud_main_rds", Address: "rm-demo0001.mysql.rds.aliyuncs.com", Port: 3306,
			Tags: []string{"ON", "主实例", "MySQL 8.0", "cn-hangzhou"},
			Meta: map[string]string{
				"iid": "rm-demo0001", "name": "prod-mysql", "region": "华东1(杭州)",
				"group": "默认资源组", "instance": "rm-demo0001.mysql.rds.aliyuncs.com:3306",
				"account": "main", "itype": "主实例", "vendor": "阿里云", "disk": "100GB",
				"cpu": "4核", "mem": "8GB", "ver": "MySQL 8.0", "domain": "rm-demo0001.mysql.rds.aliyuncs.com",
				"exp": "按量", "stat": "on",
			},
		},
		{ID: "r-demo0001", Name: "alicloud_main_redis", Address: "r-demo0001.redis.rds.aliyuncs.com", Port: 6379,
			Tags: []string{"ON", "主从", "Redis 6.0", "cn-hangzhou"},
			Meta: map[string]string{
				"iid": "r-demo0001", "name": "prod-redis", "region": "华东1(杭州)",
				"group": "默认资源组", "instance": "r-demo0001.redis.rds.aliyuncs.com:6379",
				"account": "main", "itype": "主从", "vendor": "阿里云", "mem": "4GB",
				"ver": "Redis 6.0", "exp": "按量", "stat": "on", "ip": "10.0.0.21",
			},
		},
	}
	return seed, services, nil
}
