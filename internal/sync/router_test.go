package sync

import (
	"context"
	"testing"
)

type recordingService struct {
	vendor string
}

func (s *recordingService) Group(_ context.Context, vendor, _ string) error {
	s.vendor = vendor
	return nil
}

func (s *recordingService) ECS(_ context.Context, vendor, _, _ string, _ bool) error {
	s.vendor = vendor
	return nil
}

func (s *recordingService) RDS(_ context.Context, vendor, _, _ string) error {
	s.vendor = vendor
	return nil
}

func (s *recordingService) Redis(_ context.Context, vendor, _, _ string) error {
	s.vendor = vendor
	return nil
}

func TestVendorRouterDispatchesPVEAndKeepsCloudDefault(t *testing.T) {
	cloud := &recordingService{}
	pve := &recordingService{}
	router := NewVendorRouter(cloud, map[string]Service{"pve": pve})

	if err := router.ECS(context.Background(), "pve", "demo", "cluster", false); err != nil {
		t.Fatal(err)
	}
	if pve.vendor != "pve" || cloud.vendor != "" {
		t.Fatalf("PVE routing mismatch: pve=%q cloud=%q", pve.vendor, cloud.vendor)
	}

	if err := router.ECS(context.Background(), "alicloud", "demo", "cn-hangzhou", false); err != nil {
		t.Fatal(err)
	}
	if cloud.vendor != "alicloud" {
		t.Fatalf("default routing vendor=%q, want alicloud", cloud.vendor)
	}
}
