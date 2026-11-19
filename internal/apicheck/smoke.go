package apicheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var (
	nextTime = regexp.MustCompile(`^\d{2}\.\d{2}/\d{2}:\d{2}$`)
	integer  = regexp.MustCompile(`^(0|-0|[1-9][0-9]*)$`)
)

type checkedRow map[string]any

func (r checkedRow) strings(fields ...string) error {
	for _, field := range fields {
		value, _ := r[field].(string)
		if value == "" {
			return fmt.Errorf("%s must be a nonempty string", field)
		}
	}
	return nil
}

func (r checkedRow) counts(fields ...string) error {
	for _, field := range fields {
		if !isCount(r[field]) {
			return fmt.Errorf("%s must be a nonnegative integer", field)
		}
	}
	return nil
}

func isCount(value any) bool {
	number, ok := value.(json.Number)
	return ok && integer.MatchString(string(number))
}

func (r checkedRow) nextTime() error {
	value, _ := r["nextime"].(string)
	if value != "无" && !nextTime.MatchString(value) {
		return errors.New("nextime must use MM.DD/HH:MM or 无")
	}
	return nil
}

func jobRow(r checkedRow) error {
	if err := r.strings("region", "vendor", "account", "itype", "jobid", "nextime", "runtime"); err != nil {
		return err
	}
	parts := 4
	switch r["itype"] {
	case "group":
		parts = 3
	case "ecs", "rds", "redis":
	default:
		return errors.New("unexpected itype")
	}
	if len(strings.Split(r["jobid"].(string), "/")) != parts {
		return errors.New("unexpected jobid segment count")
	}
	interval := r["interval"]
	if !isCount(interval) || interval == json.Number("0") || interval == json.Number("-0") {
		return errors.New("interval must be a positive integer")
	}
	if err := r.counts("on", "off"); err != nil {
		return err
	}
	if count := r["count"]; count != "无" && !isCount(count) {
		return errors.New("count must be a nonnegative integer or 无")
	}
	return r.nextTime()
}

func jmsRow(r checkedRow) error {
	if err := r.strings("vendor", "account"); err != nil {
		return err
	}
	if err := r.counts("count_linux", "count_win", "count_ecs", "count_on", "count_off"); err != nil {
		return err
	}
	memory, _ := r["count_mem"].(string)
	cpu, _ := r["count_cpu"].(string)
	if !strings.HasSuffix(memory, "GB") || !strings.HasSuffix(cpu, "核") {
		return errors.New("unexpected count_mem or count_cpu units")
	}
	if _, ok := r["sync"].(bool); !ok {
		return errors.New("sync must be a boolean")
	}
	return r.nextTime()
}

func resourceRow(r checkedRow) error {
	return r.strings("iid", "name", "region", "account", "vendor", "stat")
}

func rows(response object, field string, validate func(checkedRow) error) ([]object, error) {
	list, ok := response[field].([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", field)
	}
	result := make([]object, 0, len(list))
	for i, entry := range list {
		row, ok := entry.(object)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be an object", field, i)
		}
		if err := validate(checkedRow(row)); err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", field, i, err)
		}
		result = append(result, row)
	}
	return result, nil
}

func checkLists(response object, rows []object, fields ...string) error {
	for _, field := range fields {
		unique := make(map[string]struct{})
		for _, row := range rows {
			unique[row[field].(string)] = struct{}{}
		}
		want := make([]string, 0, len(unique))
		for value := range unique {
			want = append(want, value)
		}
		slices.Sort(want)
		list := field + "_list"
		got, ok := response[list].([]any)
		if !ok || !slices.EqualFunc(got, want, func(value any, expected string) bool {
			return value == expected
		}) {
			return fmt.Errorf("%s must match the sorted unique row values", list)
		}
	}
	return nil
}

func checkCloud(response object, jobs []object) error {
	cloud, ok := response["cloud_dict"].(object)
	if !ok {
		return errors.New("cloud_dict must be an object")
	}
	expected := make(map[string]map[string]struct{})
	for _, row := range jobs {
		parts := strings.Split(row["jobid"].(string), "/")
		vendor, account := parts[0], parts[1]
		if expected[vendor] == nil {
			expected[vendor] = make(map[string]struct{})
		}
		expected[vendor][account] = struct{}{}
	}
	if len(cloud) != len(expected) {
		return errors.New("cloud_dict vendors differ from job IDs")
	}
	for vendor, entry := range cloud {
		accounts, ok := entry.([]any)
		if !ok {
			return errors.New("cloud_dict accounts must be an array")
		}
		actual := make(map[string]struct{})
		for _, entry := range accounts {
			account, ok := entry.(string)
			if !ok || account == "" {
				return errors.New("cloud_dict account must be a nonempty string")
			}
			actual[account] = struct{}{}
		}
		wanted, exists := expected[vendor]
		if !exists || !maps.Equal(actual, wanted) {
			return errors.New("cloud_dict accounts differ from job IDs")
		}
	}
	return nil
}

func withQuery(name, value string) string {
	return "?" + url.Values{name: {value}}.Encode()
}

// CheckSmoke validates the jobs, resources and JumpServer read endpoints after login.
func (c *Client) CheckSmoke() error {
	const jobsPath = "/api/jobs"
	jobs, err := c.get(jobsPath, withQuery("query_dict", `{"vendor":"","account":"","itype":""}`))
	if err != nil {
		return err
	}
	allJobs, err := rows(jobs, "all_jobs", jobRow)
	if err != nil {
		return fmt.Errorf("%s: %w", jobsPath, err)
	}
	if err := checkLists(jobs, allJobs, "vendor", "account", "itype"); err != nil {
		return fmt.Errorf("%s: %w", jobsPath, err)
	}
	ecs := slices.IndexFunc(allJobs, func(row object) bool { return row["itype"] == "ecs" })
	if ecs < 0 {
		return errors.New("/api/jobs: all_jobs must contain an ECS job")
	}

	const cloudPath = "/api/edit/cloud"
	cloud, err := c.get(cloudPath, "")
	if err != nil {
		return err
	}
	if err := checkCloud(cloud, allJobs); err != nil {
		return fmt.Errorf("%s: %w", cloudPath, err)
	}

	const resPath = "/api/nodes/res"
	res, err := c.get(resPath, withQuery("job_id", allJobs[ecs]["jobid"].(string)))
	if err != nil {
		return err
	}
	if _, err := rows(res, "res_list", resourceRow); err != nil {
		return fmt.Errorf("%s: %w", resPath, err)
	}

	const jmsPath = "/api/jms/list"
	jms, err := c.get(jmsPath, withQuery("query_dict", `{"vendor":"","account":""}`))
	if err != nil {
		return err
	}
	ecsList, err := rows(jms, "ecs_list", jmsRow)
	if err != nil {
		return fmt.Errorf("%s: %w", jmsPath, err)
	}
	if err := checkLists(jms, ecsList, "vendor", "account"); err != nil {
		return fmt.Errorf("%s: %w", jmsPath, err)
	}

	const configPath = "/api/jms/config"
	config, err := c.get(configPath, "")
	if err != nil {
		return err
	}
	jmsConfig, ok := config["jms_config"].(object)
	if !ok {
		return errors.New("/api/jms/config: jms_config must be an object")
	}
	for _, field := range []string{"access_key_secret", "token"} {
		if _, exists := jmsConfig[field]; exists {
			return fmt.Errorf("%s: jms_config exposes %s", configPath, field)
		}
	}
	return nil
}
