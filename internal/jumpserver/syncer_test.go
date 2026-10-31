package jumpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lianshenyi/TenSunS-go/internal/consulmock"
	"github.com/lianshenyi/TenSunS-go/internal/cryptox"
	"github.com/lianshenyi/TenSunS-go/internal/store"
	"github.com/lianshenyi/TenSunS-go/internal/store/consul"
)

// fakeJMS is a stateful JumpServer stub covering the V2/V3 surface the syncer
// uses: node children, assets/hosts CRUD, the node tree and the paged count.
type fakeJMS struct {
	t  *testing.T
	mu sync.Mutex

	nodes  []jmsNode
	assets []jmsAsset
	nextID int

	requests      []string         // "METHOD /path" in arrival order
	payloads      []map[string]any // POST/PUT bodies in arrival order
	deletedAssets []string
	deletedNodes  []string

	// countOverride, when non-zero, replaces the computed asset count.
	countOverride int

	failChildrenStatus int
	failChildrenBody   string
	failAssetsStatus   int
	failAssetsBody     string
	failCountStatus    int
	failCountBody      string
	failTreeStatus     int
	failTreeBody       string
	failWriteStatus    int
	failWriteBody      string
}

func newFakeJMS(t *testing.T) *fakeJMS {
	return &fakeJMS{t: t}
}

func (f *fakeJMS) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/assets/assets/", f.handleAssets)
	mux.HandleFunc("/api/v1/assets/hosts/", f.handleHosts)
	mux.HandleFunc("/api/v1/assets/nodes/", f.handleNodes)
	return mux
}

func (f *fakeJMS) record(r *http.Request, payload map[string]any) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if payload != nil {
		f.payloads = append(f.payloads, payload)
	}
}

func (f *fakeJMS) nodeIDs(payload map[string]any) []string {
	raw, _ := payload["nodes"].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if id, ok := item.(string); ok {
			out = append(out, id)
		}
	}
	return out
}

// nodeValue resolves the payload node id back to its value so stored assets
// carry a usable nodes_display path.
func (f *fakeJMS) nodeValue(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	for _, node := range f.nodes {
		if node.ID == ids[0] {
			return node.Value
		}
	}
	return ids[0]
}

// treeCounts counts assets per node by the raw last path segment, mirroring
// how the tree endpoint reports "name (N)".
func (f *fakeJMS) treeCounts() map[string]int {
	counts := map[string]int{}
	for _, asset := range f.assets {
		path := asset.nodePath()
		if path == "" {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		counts[name]++
	}
	return counts
}

func (f *fakeJMS) handleAssets(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/assets/assets/"), "/")
	switch {
	case id == "" && r.Method == http.MethodGet && r.URL.Query().Get("limit") != "":
		if f.failCountStatus != 0 {
			w.WriteHeader(f.failCountStatus)
			_, _ = io.WriteString(w, f.failCountBody)
			return
		}
		count := len(f.assets)
		if f.countOverride > 0 {
			count = f.countOverride
		}
		respondJSON(w, map[string]any{"count": count, "results": []any{}})
	case id == "" && r.Method == http.MethodGet:
		if f.failAssetsStatus != 0 {
			w.WriteHeader(f.failAssetsStatus)
			_, _ = io.WriteString(w, f.failAssetsBody)
			return
		}
		respondJSON(w, f.assets)
	case id == "" && r.Method == http.MethodPost:
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		f.record(r, payload)
		if f.failWriteStatus != 0 {
			w.WriteHeader(f.failWriteStatus)
			_, _ = io.WriteString(w, f.failWriteBody)
			return
		}
		f.nextID++
		f.assets = append(f.assets, jmsAsset{
			ID:           "asset-" + strconv.Itoa(f.nextID),
			IP:           stringField(payload["ip"]),
			Hostname:     stringField(payload["hostname"]),
			NodesDisplay: []string{f.nodeValue(f.nodeIDs(payload))},
		})
		w.WriteHeader(http.StatusCreated)
	case id != "" && r.Method == http.MethodPut:
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		f.record(r, payload)
		if f.failWriteStatus != 0 {
			w.WriteHeader(f.failWriteStatus)
			_, _ = io.WriteString(w, f.failWriteBody)
			return
		}
		for index := range f.assets {
			if f.assets[index].ID == id {
				f.assets[index].IP = stringField(payload["ip"])
				f.assets[index].Hostname = stringField(payload["hostname"])
				f.assets[index].NodesDisplay = []string{f.nodeValue(f.nodeIDs(payload))}
			}
		}
		w.WriteHeader(http.StatusOK)
	case id != "" && r.Method == http.MethodDelete:
		f.record(r, nil)
		if f.failWriteStatus != 0 {
			w.WriteHeader(f.failWriteStatus)
			_, _ = io.WriteString(w, f.failWriteBody)
			return
		}
		f.deletedAssets = append(f.deletedAssets, id)
		for index := 0; index < len(f.assets); index++ {
			if f.assets[index].ID == id {
				f.assets = append(f.assets[:index], f.assets[index+1:]...)
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeJMS) handleHosts(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/assets/hosts/"), "/")
	switch {
	case id == "" && r.Method == http.MethodPost:
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		f.record(r, payload)
		if f.failWriteStatus != 0 {
			w.WriteHeader(f.failWriteStatus)
			_, _ = io.WriteString(w, f.failWriteBody)
			return
		}
		f.nextID++
		f.assets = append(f.assets, jmsAsset{
			ID:           "asset-" + strconv.Itoa(f.nextID),
			Address:      stringField(payload["address"]),
			Name:         stringField(payload["name"]),
			NodesDisplay: []string{f.nodeValue(f.nodeIDs(payload))},
		})
		w.WriteHeader(http.StatusCreated)
	case id != "" && r.Method == http.MethodPut:
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		f.record(r, payload)
		if f.failWriteStatus != 0 {
			w.WriteHeader(f.failWriteStatus)
			_, _ = io.WriteString(w, f.failWriteBody)
			return
		}
		for index := range f.assets {
			if f.assets[index].ID == id {
				f.assets[index].Address = stringField(payload["address"])
				f.assets[index].Name = stringField(payload["name"])
				f.assets[index].NodesDisplay = []string{f.nodeValue(f.nodeIDs(payload))}
			}
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeJMS) handleNodes(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	switch {
	case path == "/api/v1/assets/nodes/children/tree/" && r.Method == http.MethodGet:
		if f.failTreeStatus != 0 {
			w.WriteHeader(f.failTreeStatus)
			_, _ = io.WriteString(w, f.failTreeBody)
			return
		}
		counts := f.treeCounts()
		tree := make([]map[string]any, 0, len(f.nodes))
		for _, node := range f.nodes {
			tree = append(tree, map[string]any{
				"id":   node.ID,
				"name": fmt.Sprintf("%s (%d)", node.Value, counts[node.Value]),
				"meta": map[string]any{"data": map[string]any{"id": node.ID}},
			})
		}
		respondJSON(w, tree)
	case strings.HasSuffix(path, "/children/") && r.Method == http.MethodGet:
		if f.failChildrenStatus != 0 {
			w.WriteHeader(f.failChildrenStatus)
			_, _ = io.WriteString(w, f.failChildrenBody)
			return
		}
		respondJSON(w, f.nodes)
	case strings.HasSuffix(path, "/children/") && r.Method == http.MethodPost:
		var body struct {
			Value string `json:"value"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.record(r, map[string]any{"value": body.Value})
		f.nextID++
		f.nodes = append(f.nodes, jmsNode{ID: "node-" + strconv.Itoa(f.nextID), Value: body.Value})
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodDelete:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/assets/nodes/"), "/")
		f.record(r, nil)
		f.deletedNodes = append(f.deletedNodes, id)
		for index := 0; index < len(f.nodes); index++ {
			if f.nodes[index].ID == id {
				f.nodes = append(f.nodes[:index], f.nodes[index+1:]...)
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

func respondJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func stringField(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// jmsTestStore wires a consul mock with the JMS configuration shared by the
// tests; tests add group KVs, custom rules and services on top.
type jmsTestStore struct {
	st     store.Store
	cipher *cryptox.Cipher
	ctx    context.Context
	http   *http.Client
}

func newJMSStore(t *testing.T, fake *fakeJMS, version string) *jmsTestStore {
	t.Helper()
	jms := httptest.NewServer(fake.handler())
	t.Cleanup(jms.Close)
	mock := consulmock.New()
	consulServer := httptest.NewServer(mock.Handler())
	t.Cleanup(consulServer.Close)
	st := consul.New(consulServer.URL+"/v1", "", consulServer.Client())
	cipher, err := cryptox.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.Encrypt("jumpserver-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for path, value := range map[string]any{
		"ConsulManager/jms/jms_info":        map[string]any{"url": jms.URL, "token": token, "ver": version},
		"ConsulManager/jms/pve/lab/node_id": map[string]any{"node_id": "node-1"},
		"ConsulManager/jms/ecs_info":        map[string]any{"linux": []any{[]string{"ssh/22"}, "root"}, "windows": []any{[]string{"rdp/3389"}, "windows-template"}},
	} {
		if err := st.PutKV(ctx, path, value); err != nil {
			t.Fatal(err)
		}
	}
	return &jmsTestStore{st: st, cipher: cipher, ctx: ctx, http: jms.Client()}
}

func (j *jmsTestStore) registerService(t *testing.T, id, name, address, group, osType string) {
	t.Helper()
	if err := j.st.Register(j.ctx, store.ServiceRegistration{
		ID: id, Name: "pve_lab_ecs", Address: address,
		Meta: map[string]string{"name": name, "os": osType, "group": group, "region": "lab", "vendor": "Proxmox VE"},
	}); err != nil {
		t.Fatal(err)
	}
}

func (j *jmsTestStore) putKV(t *testing.T, path string, value any) {
	t.Helper()
	if err := j.st.PutKV(j.ctx, path, value); err != nil {
		t.Fatal(err)
	}
}

func (j *jmsTestStore) getValue(t *testing.T, path string, out any) bool {
	t.Helper()
	found, err := j.st.GetValue(j.ctx, path, out)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func (j *jmsTestStore) run(t *testing.T) error {
	t.Helper()
	return New(j.st, j.cipher, j.http).Run(j.ctx, "pve", "lab")
}

func assertJSONField(t *testing.T, object map[string]any, key string, want any) {
	t.Helper()
	actualJSON, err := json.Marshal(object[key])
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(actualJSON) != string(wantJSON) {
		t.Fatalf("%s = %s, want %s", key, actualJSON, wantJSON)
	}
}

func lastPayload(t *testing.T, fake *fakeJMS) map[string]any {
	t.Helper()
	if len(fake.payloads) == 0 {
		t.Fatal("JumpServer did not receive a write request")
	}
	return fake.payloads[len(fake.payloads)-1]
}

func TestV2MockContractPayloadUsesECSInfo(t *testing.T) {
	fake := newFakeJMS(t)
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "prod", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	payload := lastPayload(t, fake)
	if payload["ip"] != "10.0.0.20" || payload["hostname"] != "app-01" || payload["platform"] != "Linux" {
		t.Fatalf("V2 identity payload = %#v", payload)
	}
	assertJSONField(t, payload, "protocols", []any{"ssh/22"})
	if payload["admin_user"] != "root" || payload["domain"] != "" || payload["is_active"] != true {
		t.Fatalf("V2 access payload = %#v", payload)
	}
}

func TestQAV3PayloadIncludesProtocolsAndAccounts(t *testing.T) {
	fake := newFakeJMS(t)
	store := newJMSStore(t, fake, "V3")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "prod", "windows")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	payload := lastPayload(t, fake)
	assertJSONField(t, payload, "protocols", []any{map[string]any{"name": "rdp", "port": float64(3389)}, map[string]any{"name": "sftp", "port": float64(3389)}})
	assertJSONField(t, payload, "accounts", []any{map[string]any{"name": "windows-template", "username": "windows-template"}})
	if account := payload["accounts"].([]any)[0].(map[string]any); account["template"] != nil {
		t.Fatalf("V3 username was incorrectly sent as an account template: %#v", account)
	}
	if payload["platform"] != "5" || payload["domain"] != "" || payload["address"] != "10.0.0.20" || payload["name"] != "app-01" {
		t.Fatalf("V3 payload = %#v", payload)
	}
}

// TestV2V3AssetCreateUpdateHTTPContracts pins the baseline endpoints: both
// versions update through the version-specific detail endpoint (V2 assets,
// V3 hosts), matching sync_jms.py rather than the older assumption of a
// shared /api/v1/assets/{id}/ update route.
func TestV2V3AssetCreateUpdateHTTPContracts(t *testing.T) {
	tests := []struct {
		name          string
		version       string
		existing      bool
		wantMethod    string
		wantAssetPath string
	}{
		{name: "v2_create", version: "V2", wantMethod: http.MethodPost, wantAssetPath: "/api/v1/assets/assets/"},
		{name: "v2_update", version: "V2", existing: true, wantMethod: http.MethodPut, wantAssetPath: "/api/v1/assets/assets/asset-seed/"},
		{name: "v3_create", version: "V3", wantMethod: http.MethodPost, wantAssetPath: "/api/v1/assets/hosts/"},
		{name: "v3_update", version: "V3", existing: true, wantMethod: http.MethodPut, wantAssetPath: "/api/v1/assets/hosts/asset-seed/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeJMS(t)
			if tt.existing {
				asset := jmsAsset{ID: "asset-seed", Hostname: "old-name", Name: "old-name", NodesDisplay: []string{"未分组"}}
				if tt.version == "V3" {
					asset.Address = "10.0.0.20"
				} else {
					asset.IP = "10.0.0.20"
				}
				fake.assets = append(fake.assets, asset)
				fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
			}
			store := newJMSStore(t, fake, tt.version)
			store.registerService(t, "vm-1", "app-01", "10.0.0.20", "prod", "linux")
			if err := store.run(t); err != nil {
				t.Fatal(err)
			}
			method, path := "", ""
			for _, request := range fake.requests {
				parts := strings.SplitN(request, " ", 2)
				if parts[0] == http.MethodPost || parts[0] == http.MethodPut {
					method, path = parts[0], parts[1]
				}
			}
			if method != tt.wantMethod || path != tt.wantAssetPath {
				t.Fatalf("asset request = %s %s, want %s %s", method, path, tt.wantMethod, tt.wantAssetPath)
			}
		})
	}
}

func TestGroupNodesCreatedAndAssetsAttached(t *testing.T) {
	fake := newFakeJMS(t)
	store := newJMSStore(t, fake, "V2")
	store.putKV(t, "ConsulManager/assets/pve/group/lab", map[string]string{"g-1": "prod", "g-2": "dev"})
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "prod", "linux")
	store.registerService(t, "vm-2", "app-02", "10.0.0.21", "dev", "linux")
	store.registerService(t, "vm-3", "app-03", "10.0.0.22", "missing", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	// One node creation per missing group value plus 未分组.
	wantCreated := map[string]bool{"prod": false, "dev": false, "未分组": false}
	for _, payload := range fake.payloads {
		if value, ok := payload["value"].(string); ok {
			if _, tracked := wantCreated[value]; tracked {
				wantCreated[value] = true
			}
		}
	}
	for name, created := range wantCreated {
		if !created {
			t.Fatalf("node %q was not created; payloads=%v", name, fake.payloads)
		}
	}
	nodeByValue := map[string]string{}
	for _, node := range fake.nodes {
		nodeByValue[node.Value] = node.ID
	}
	for _, payload := range fake.payloads {
		if _, ok := payload["value"]; ok {
			continue
		}
		switch stringField(payload["hostname"]) {
		case "app-01":
			assertJSONField(t, payload, "nodes", []any{nodeByValue["prod"]})
		case "app-02":
			assertJSONField(t, payload, "nodes", []any{nodeByValue["dev"]})
		case "app-03":
			assertJSONField(t, payload, "nodes", []any{nodeByValue["未分组"]})
		}
	}
}

func TestExistingGroupNodesNotRecreated(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	for _, payload := range fake.payloads {
		if _, ok := payload["value"]; ok {
			t.Fatalf("existing node was recreated: %v", payload)
		}
	}
}

func TestStaleAssetDeleted(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
	fake.assets = append(fake.assets,
		jmsAsset{ID: "asset-stale", IP: "10.0.0.99", Hostname: "ghost", NodesDisplay: []string{"未分组"}},
		jmsAsset{ID: "asset-keep", IP: "10.0.0.20", Hostname: "app-01", NodesDisplay: []string{"未分组"}},
	)
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.deletedAssets, []string{"asset-stale"}) {
		t.Fatalf("deleted assets = %v, want [asset-stale]", fake.deletedAssets)
	}
}

func TestDuplicateIPNameMismatchDeleted(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
	// The matching asset is listed last so the update decision sees it; the
	// ghost duplicate is removed by the repip cleanup.
	fake.assets = append(fake.assets,
		jmsAsset{ID: "asset-ghost", IP: "10.0.0.20", Hostname: "ghost", NodesDisplay: []string{"未分组"}},
		jmsAsset{ID: "asset-keep", IP: "10.0.0.20", Hostname: "app-01", NodesDisplay: []string{"未分组"}},
	)
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.deletedAssets, []string{"asset-ghost"}) {
		t.Fatalf("deleted assets = %v, want [asset-ghost]", fake.deletedAssets)
	}
}

func TestDuplicateIPMatchingNamesKept(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
	fake.assets = append(fake.assets,
		jmsAsset{ID: "asset-a", IP: "10.0.0.20", Hostname: "app-01", NodesDisplay: []string{"未分组"}},
		jmsAsset{ID: "asset-b", IP: "10.0.0.20", Hostname: "app-01", NodesDisplay: []string{"未分组"}},
	)
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	if len(fake.deletedAssets) != 0 {
		t.Fatalf("matching duplicates were deleted: %v", fake.deletedAssets)
	}
}

func TestEmptyGroupNodesDeleted(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes,
		jmsNode{ID: "node-empty", Value: "empty"},
		jmsNode{ID: "node-busy", Value: "busy"},
	)
	fake.assets = append(fake.assets,
		jmsAsset{ID: "asset-1", IP: "10.0.0.20", Hostname: "app-01", NodesDisplay: []string{"busy"}},
	)
	store := newJMSStore(t, fake, "V2")
	store.putKV(t, "ConsulManager/assets/pve/group/lab", map[string]string{"g-1": "empty", "g-2": "busy"})
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "busy", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	// The empty group node is removed; the busy one survives. The 未分组 node
	// created by ensureNodes is also empty here and gets cleaned up, matching
	// the baseline's create-then-prune behavior.
	deleted := map[string]bool{}
	for _, id := range fake.deletedNodes {
		deleted[id] = true
	}
	if !deleted["node-empty"] {
		t.Fatalf("empty node was not deleted; deleted nodes = %v", fake.deletedNodes)
	}
	if deleted["node-busy"] {
		t.Fatalf("busy node was deleted; deleted nodes = %v", fake.deletedNodes)
	}
}

func TestCustomECSInfoOverridesByHostname(t *testing.T) {
	fake := newFakeJMS(t)
	store := newJMSStore(t, fake, "V2")
	store.putKV(t, "ConsulManager/jms/pve/lab/custom_ecs_info", map[string]any{
		"prod-web": map[string]any{"linux": []any{[]string{"ssh/2222"}, "customuser"}},
	})
	store.registerService(t, "vm-1", "prod-web-01", "10.0.0.20", "prod", "linux")
	store.registerService(t, "vm-2", "db-01", "10.0.0.21", "prod", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	for _, payload := range fake.payloads {
		if _, ok := payload["value"]; ok {
			continue
		}
		switch stringField(payload["hostname"]) {
		case "prod-web-01":
			assertJSONField(t, payload, "protocols", []any{"ssh/2222"})
			if payload["admin_user"] != "customuser" {
				t.Fatalf("custom admin_user = %#v", payload)
			}
		case "db-01":
			assertJSONField(t, payload, "protocols", []any{"ssh/22"})
			if payload["admin_user"] != "root" {
				t.Fatalf("default admin_user = %#v", payload)
			}
		}
	}
}

func TestCustomECSInfoFallsBackToGlobal(t *testing.T) {
	fake := newFakeJMS(t)
	store := newJMSStore(t, fake, "V2")
	store.putKV(t, "ConsulManager/jms/custom_ecs_info", map[string]any{
		"prod-web": map[string]any{"linux": []any{[]string{"ssh/2222"}, "customuser"}},
	})
	store.registerService(t, "vm-1", "prod-web-01", "10.0.0.20", "prod", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	payload := lastPayload(t, fake)
	assertJSONField(t, payload, "protocols", []any{"ssh/2222"})
	if payload["admin_user"] != "customuser" {
		t.Fatalf("global custom admin_user = %#v", payload)
	}
}

func TestAccountCustomECSInfoPreferredOverGlobal(t *testing.T) {
	fake := newFakeJMS(t)
	store := newJMSStore(t, fake, "V2")
	store.putKV(t, "ConsulManager/jms/pve/lab/custom_ecs_info", map[string]any{
		"prod-web": map[string]any{"linux": []any{[]string{"ssh/2222"}, "account-user"}},
	})
	store.putKV(t, "ConsulManager/jms/custom_ecs_info", map[string]any{
		"prod-web": map[string]any{"linux": []any{[]string{"ssh/3333"}, "global-user"}},
	})
	store.registerService(t, "vm-1", "prod-web-01", "10.0.0.20", "prod", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	payload := lastPayload(t, fake)
	assertJSONField(t, payload, "protocols", []any{"ssh/2222"})
	if payload["admin_user"] != "account-user" {
		t.Fatalf("account-level custom admin_user = %#v", payload)
	}
}

func TestV3ProtocolPortPreservedFromExisting(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
	fake.assets = append(fake.assets, jmsAsset{
		ID: "asset-seed", Address: "10.0.0.20", Name: "old-name",
		Protocols:    json.RawMessage(`[{"name":"ssh","port":2222}]`),
		NodesDisplay: []string{"未分组"},
	})
	store := newJMSStore(t, fake, "V3")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "prod", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	payload := lastPayload(t, fake)
	assertJSONField(t, payload, "protocols", []any{map[string]any{"name": "ssh", "port": float64(2222)}, map[string]any{"name": "sftp", "port": float64(2222)}})
}

func TestAssetMovedOnGroupChange(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes,
		jmsNode{ID: "node-1", Value: "未分组"},
		jmsNode{ID: "node-2", Value: "dev"},
	)
	fake.assets = append(fake.assets, jmsAsset{
		ID: "asset-seed", IP: "10.0.0.20", Hostname: "app-01", NodesDisplay: []string{"未分组"},
	})
	store := newJMSStore(t, fake, "V2")
	store.putKV(t, "ConsulManager/assets/pve/group/lab", map[string]string{"g-1": "dev"})
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "dev", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	payload := lastPayload(t, fake)
	assertJSONField(t, payload, "nodes", []any{"node-2"})
}

func TestUnchangedAssetNotUpdated(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
	fake.assets = append(fake.assets, jmsAsset{
		ID: "asset-seed", IP: "10.0.0.20", Hostname: "app-01", NodesDisplay: []string{"未分组"},
	})
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	for _, request := range fake.requests {
		if strings.HasPrefix(request, http.MethodPut) {
			t.Fatalf("unchanged asset was updated: %v", fake.requests)
		}
	}
}

func TestRecordCountComesFromJMS(t *testing.T) {
	fake := newFakeJMS(t)
	fake.countOverride = 7
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if !store.getValue(t, "ConsulManager/record/jms/pve/lab", &record) {
		t.Fatal("record was not written")
	}
	// The count must come from the JMS count endpoint, not len(services)=1.
	if record["status"] != float64(20000) || record["count"] != float64(7) || record["msg"] != "同步资源成功！总数：7" {
		t.Fatalf("record = %#v", record)
	}
	if record["update"] == nil {
		t.Fatalf("record update missing: %#v", record)
	}
}

func TestSyncFailureRecordsStatus50000(t *testing.T) {
	fake := newFakeJMS(t)
	fake.failChildrenStatus = http.StatusInternalServerError
	fake.failChildrenBody = `{"detail":"node listing exploded"}`
	store := newJMSStore(t, fake, "V2")
	if err := store.run(t); err == nil {
		t.Fatal("Run succeeded against a failing JumpServer")
	}
	var record map[string]any
	if !store.getValue(t, "ConsulManager/record/jms/pve/lab", &record) {
		t.Fatal("failure record was not written")
	}
	if record["status"] != float64(50000) || record["count"] != "失败" || record["msg"] != "同步资源失败！node listing exploded" {
		t.Fatalf("failure record = %#v", record)
	}
}

func TestMutationFailuresAreAggregatedAndRecorded(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*fakeJMS, *jmsTestStore)
	}{
		{
			name: "post",
			prepare: func(fake *fakeJMS, st *jmsTestStore) {
				fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
				st.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
			},
		},
		{
			name: "put",
			prepare: func(fake *fakeJMS, st *jmsTestStore) {
				fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
				fake.assets = append(fake.assets, jmsAsset{ID: "asset-1", IP: "10.0.0.20", Hostname: "old-name", NodesDisplay: []string{"未分组"}})
				st.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
			},
		},
		{
			name: "delete",
			prepare: func(fake *fakeJMS, st *jmsTestStore) {
				fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
				fake.assets = append(fake.assets, jmsAsset{ID: "asset-stale", IP: "10.0.0.99", Hostname: "stale", NodesDisplay: []string{"未分组"}})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeJMS(t)
			fake.failWriteStatus = http.StatusInternalServerError
			fake.failWriteBody = `{"detail":"write exploded"}`
			st := newJMSStore(t, fake, "V2")
			tt.prepare(fake, st)
			if err := st.run(t); err == nil || !strings.Contains(err.Error(), "write exploded") {
				t.Fatalf("Run error = %v, want aggregated write failure", err)
			}
			var record map[string]any
			if !st.getValue(t, "ConsulManager/record/jms/pve/lab", &record) {
				t.Fatal("failure record was not written")
			}
			if record["status"] != float64(50000) || record["count"] != "失败" || !strings.Contains(stringField(record["msg"]), "write exploded") {
				t.Fatalf("failure record = %#v", record)
			}
		})
	}
}

// TestLargeAssetListNotTruncated pins the getJSON read bound: a real
// JumpServer list with a dozen hosts already exceeds 4 KiB, and the earlier
// 4 KiB cap truncated the JSON mid-token, failing the whole sync. The 50
// listed assets make the list response well over 4 KiB: one matches a
// service and is updated, one service is created from scratch, and the 49
// stale assets are deleted.
func TestLargeAssetListNotTruncated(t *testing.T) {
	fake := newFakeJMS(t)
	fake.nodes = append(fake.nodes, jmsNode{ID: "node-1", Value: "未分组"})
	fake.assets = append(fake.assets, jmsAsset{ID: "asset-1", IP: "10.0.0.20", Hostname: "old-name", NodesDisplay: []string{"未分组"}})
	for i := 0; i < 49; i++ {
		fake.assets = append(fake.assets, jmsAsset{
			ID:           fmt.Sprintf("asset-stale-%d", i),
			IP:           fmt.Sprintf("10.9.0.%d", i+1),
			Hostname:     fmt.Sprintf("ghost-%d", i),
			NodesDisplay: []string{"未分组"},
		})
	}
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "无", "linux")
	store.registerService(t, "vm-2", "app-02", "10.0.0.30", "无", "linux")
	if err := store.run(t); err != nil {
		t.Fatal(err)
	}
	var putPaths, postPaths []string
	for _, request := range fake.requests {
		parts := strings.SplitN(request, " ", 2)
		switch parts[0] {
		case http.MethodPost:
			postPaths = append(postPaths, parts[1])
		case http.MethodPut:
			putPaths = append(putPaths, parts[1])
		}
	}
	if len(putPaths) != 1 || putPaths[0] != "/api/v1/assets/assets/asset-1/" {
		t.Fatalf("PUT requests = %v, want one update of asset-1", putPaths)
	}
	if len(postPaths) != 1 || postPaths[0] != "/api/v1/assets/assets/" {
		t.Fatalf("POST requests = %v, want one create of app-02", postPaths)
	}
	if len(fake.deletedAssets) != 49 {
		t.Fatalf("deleted assets = %d, want 49 stale assets", len(fake.deletedAssets))
	}
	hostnames := map[string]bool{}
	for _, asset := range fake.assets {
		hostnames[asset.Hostname] = true
	}
	if !hostnames["app-01"] || !hostnames["app-02"] || len(hostnames) != 2 {
		t.Fatalf("assets after sync = %v, want app-01 and app-02 only", hostnames)
	}
}

func TestSyncFailureOnCountRecordsStatus50000(t *testing.T) {
	fake := newFakeJMS(t)
	fake.failCountStatus = http.StatusInternalServerError
	fake.failCountBody = `{"detail":"count exploded"}`
	store := newJMSStore(t, fake, "V2")
	store.registerService(t, "vm-1", "app-01", "10.0.0.20", "prod", "linux")
	if err := store.run(t); err == nil {
		t.Fatal("Run succeeded against a failing count endpoint")
	}
	var record map[string]any
	if !store.getValue(t, "ConsulManager/record/jms/pve/lab", &record) {
		t.Fatal("failure record was not written")
	}
	if record["status"] != float64(50000) || record["msg"] != "同步资源失败！count exploded" {
		t.Fatalf("failure record = %#v", record)
	}
}
