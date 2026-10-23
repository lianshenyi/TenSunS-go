package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"

	"github.com/lianshenyi/TenSunS-go/internal/scheduler"
	"github.com/lianshenyi/TenSunS-go/internal/store"
)

// LoadFromConsul loads both cloud and JMS task KVs into the scheduler.
//
// Unknown func strings (for example stale exp/avd jobs) are logged and
// skipped. Their KV is left untouched so a rollback to Python remains safe.
func LoadFromConsul(ctx context.Context, st store.Store, sched *scheduler.Scheduler, logger *slog.Logger) (loaded, skipped int, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	cloudJobs, err := st.GetKVDict(ctx, "ConsulManager/jobs")
	if err != nil {
		return 0, 0, err
	}
	jmsJobs, err := st.GetKVDict(ctx, "ConsulManager/jms/jobs")
	if err != nil {
		return 0, 0, err
	}
	all := make(map[string]json.RawMessage, len(cloudJobs)+len(jmsJobs))
	for key, raw := range cloudJobs {
		all[key] = raw
	}
	for key, raw := range jmsJobs {
		all[key] = raw
	}
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		spec, err := scheduler.DecodeJobSpec(all[key])
		if err != nil {
			logger.Warn("skip invalid job KV", "key", key, "error", err)
			skipped++
			continue
		}
		if _, err := scheduler.ParseFunc(spec.Func); err != nil {
			logger.Warn("skip unknown legacy func", "id", spec.ID, "func", spec.Func)
			skipped++
			continue
		}
		if err := sched.Upsert(ctx, spec); err != nil {
			logger.Warn("skip job that could not be scheduled", "id", spec.ID, "error", err)
			skipped++
			continue
		}
		loaded++
	}
	return loaded, skipped, nil
}
