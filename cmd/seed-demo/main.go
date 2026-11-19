// Command seed-demo writes the demo fixture to a disposable Consul instance.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/lianshenyi/TenSunS-go/internal/demodata"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) > 3 {
		log.Fatal("usage: seed-demo [consul_url] [token]")
	}

	baseURL, ok := os.LookupEnv("CONSUL_URL")
	if !ok {
		baseURL = "http://127.0.0.1:18500/v1"
	}
	token := os.Getenv("CONSUL_TOKEN")
	if len(os.Args) >= 2 {
		baseURL = os.Args[1]
	}
	if len(os.Args) == 3 {
		token = os.Args[2]
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if err := seedDemo(context.Background(), consul.New(baseURL, token, client)); err != nil {
		log.Fatal(err)
	}
}

func seedDemo(ctx context.Context, target *consul.Store) error {
	values, services, err := demodata.New("ConsulManager/jms/jobs/alicloud/main/node_id")
	if err != nil {
		return err
	}

	// Keep the write order stable when diagnosing and retrying a partial seed.
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := target.PutKV(ctx, key, values[key]); err != nil {
			return fmt.Errorf("seed %s: %w", key, err)
		}
	}
	for _, service := range services {
		if err := target.Register(ctx, service); err != nil {
			return fmt.Errorf("seed service %s: %w", service.ID, err)
		}
	}
	fmt.Printf("seeded %d keys and %d services\n", len(values), len(services))
	return nil
}
