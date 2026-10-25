package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/cloud"
	"github.com/lianshenyi/TenSunS-go/internal/cloud/model"
)

type testProvider struct{}

func (testProvider) Group(context.Context, string) (map[string]string, error) {
	return nil, nil
}

func (testProvider) ECS(context.Context, string, string, bool) (map[string]model.ECS, error) {
	return nil, nil
}

func (testProvider) RDS(context.Context, string, string) (map[string]model.RDS, error) {
	return nil, nil
}

func (testProvider) Redis(context.Context, string, string) (map[string]model.Redis, error) {
	return nil, nil
}

func TestLogCloudCapabilities(t *testing.T) {
	registry := cloud.NewRegistry()
	registry.Register("alicloud", testProvider{})

	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logCloudCapabilities(logger, registry)

	logs := output.String()
	if !strings.Contains(logs, "resource group providers registered") || !strings.Contains(logs, "providers=[alicloud]") {
		t.Fatalf("provider registration log = %q", logs)
	}
	if !strings.Contains(logs, "ECS, RDS, and Redis task execution is not implemented") {
		t.Fatalf("execution limitation log = %q", logs)
	}
	if strings.Contains(logs, "provider registry is empty") {
		t.Fatalf("misleading empty-registry log = %q", logs)
	}
}
