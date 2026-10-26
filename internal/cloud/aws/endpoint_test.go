package awscloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func TestEC2SDKUsesHTTPEndpointFixture(t *testing.T) {
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><requestId>x</requestId><reservationSet/></DescribeInstancesResponse>`))
	}))
	defer fixture.Close()
	config, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"), awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("ak", "sk", "")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ec2.NewFromConfig(config, func(options *ec2.Options) { options.BaseEndpoint = aws.String(fixture.URL) }).DescribeInstances(context.Background(), &ec2.DescribeInstancesInput{}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("fixture requests = %d", requests.Load())
	}
}
