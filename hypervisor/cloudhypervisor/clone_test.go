package cloudhypervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/projecteru2/cocoon/types"
)

// helpers

// writeCHConfig writes a JSON config to path and returns the path.
func writeCHConfig(t *testing.T, dir string, cfg map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readRawJSON reads a JSON file back into a generic map.
func readRawJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// baseCHConfig returns a minimal CH config.json with extra fields that CH adds internally.
func baseCHConfig() map[string]any {
	return map[string]any{
		"payload": map[string]any{
			"kernel":    "/boot/vmlinux",
			"initramfs": "/boot/initrd",
			"cmdline":   "console=hvc0 old-cmdline",
		},
		"cpus": map[string]any{
			"boot_vcpus":   2,
			"max_vcpus":    8,
			"topology":     nil,
			"max_phys_bits": 46,
		},
		"memory": map[string]any{
			"size":           int64(1 << 30),
			"hugepages":      false,
			"hotplug_method": "Acpi",
			"shared":         false,
			"thp":            true,
		},
		"disks": []any{
			map[string]any{
				"id":                "_disk0",
				"path":             "/old/layer.erofs",
				"readonly":         true,
				"serial":           "layer0",
				"pci_segment":      0,
				"disable_io_uring": false,
			},
			map[string]any{
				"id":                "_disk1",
				"path":             "/old/cow.raw",
				"readonly":         false,
				"serial":           "cocoon-cow",
				"pci_segment":      0,
				"disable_io_uring": false,
				"sparse":           true,
			},
		},
		"net": []any{
			map[string]any{
				"id":          "_net0",
				"tap":         "old-tap0",
				"mac":         "aa:bb:cc:dd:ee:f0",
				"num_queues":  4,
				"queue_size":  256,
				"ip":          nil,
				"mask":        nil,
				"pci_segment": 0,
			},
		},
		"balloon": map[string]any{
			"id":                  "_balloon0",
			"size":                int64(1 << 30) / 4,
			"deflate_on_oom":      true,
			"free_page_reporting": true,
		},
		"serial": map[string]any{
			"mode": "Socket",
			"socket": "/old/console.sock",
		},
		"console": map[string]any{
			"mode": "Off",
		},
		"rng": map[string]any{
			"src": "/dev/urandom",
		},
		"watchdog": true,
		"platform": map[string]any{
			"num_pci_segments": 1,
			"uuid":            "12345678-1234-1234-1234-123456789abc",
			"serial_number":   nil,
			"oem_strings":     nil,
		},
	}
}

func basePatchOpts() *patchOptions {
	return &patchOptions{
		storageConfigs: []*types.StorageConfig{
			{Path: "/new/layer.erofs", RO: true, Serial: "layer0"},
			{Path: "/new/cow.raw", RO: false, Serial: "cocoon-cow"},
		},
		networkConfigs: []*types.NetworkConfig{
			{Tap: "new-tap0", Mac: "11:22:33:44:55:66", NumQueues: 8, QueueSize: 512},
		},
		consoleSock: "/new/console.sock",
		directBoot:  true,
		vmName:      "test-vm",
		dnsServers:  []string{"8.8.8.8"},
	}
}

// patchCHConfig

func TestPatchCHConfig_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := writeCHConfig(t, dir, baseCHConfig())

	if err := patchCHConfig(path, basePatchOpts()); err != nil {
		t.Fatalf("patchCHConfig: %v", err)
	}

	result := readRawJSON(t, path)

	// Top-level: platform must survive.
	platform, ok := result["platform"].(map[string]any)
	if !ok {
		t.Fatal("platform section lost")
	}
	if platform["uuid"] != "12345678-1234-1234-1234-123456789abc" {
		t.Errorf("platform.uuid lost: got %v", platform["uuid"])
	}

	// cpus: topology and max_phys_bits must survive.
	cpus := result["cpus"].(map[string]any)
	if _, ok := cpus["max_phys_bits"]; !ok {
		t.Error("cpus.max_phys_bits lost")
	}

	// memory: hotplug_method, shared, thp must survive.
	mem := result["memory"].(map[string]any)
	if _, ok := mem["hotplug_method"]; !ok {
		t.Error("memory.hotplug_method lost")
	}
	if _, ok := mem["thp"]; !ok {
		t.Error("memory.thp lost")
	}

	// disks: pci_segment, disable_io_uring, id must survive.
	disks := result["disks"].([]any)
	for i, d := range disks {
		disk := d.(map[string]any)
		if _, ok := disk["pci_segment"]; !ok {
			t.Errorf("disk[%d].pci_segment lost", i)
		}
		if _, ok := disk["disable_io_uring"]; !ok {
			t.Errorf("disk[%d].disable_io_uring lost", i)
		}
		if _, ok := disk["id"]; !ok {
			t.Errorf("disk[%d].id lost", i)
		}
	}

	// net: pci_segment, id must survive.
	nets := result["net"].([]any)
	net0 := nets[0].(map[string]any)
	if net0["id"] != "_net0" {
		t.Errorf("net[0].id lost: got %v", net0["id"])
	}
	if _, ok := net0["pci_segment"]; !ok {
		t.Error("net[0].pci_segment lost")
	}
}

func TestPatchCHConfig_UpdatesDiskPaths(t *testing.T) {
	dir := t.TempDir()
	path := writeCHConfig(t, dir, baseCHConfig())

	opts := basePatchOpts()
	if err := patchCHConfig(path, opts); err != nil {
		t.Fatal(err)
	}

	result := readRawJSON(t, path)
	disks := result["disks"].([]any)

	disk0 := disks[0].(map[string]any)
	if disk0["path"] != "/new/layer.erofs" {
		t.Errorf("disk[0].path: got %v, want /new/layer.erofs", disk0["path"])
	}

	disk1 := disks[1].(map[string]any)
	if disk1["path"] != "/new/cow.raw" {
		t.Errorf("disk[1].path: got %v, want /new/cow.raw", disk1["path"])
	}

	// Original device IDs preserved.
	if disk0["id"] != "_disk0" {
		t.Errorf("disk[0].id changed: got %v", disk0["id"])
	}
}

func TestPatchCHConfig_UpdatesNetwork(t *testing.T) {
	dir := t.TempDir()
	path := writeCHConfig(t, dir, baseCHConfig())

	opts := basePatchOpts()
	if err := patchCHConfig(path, opts); err != nil {
		t.Fatal(err)
	}

	result := readRawJSON(t, path)
	net0 := result["net"].([]any)[0].(map[string]any)

	if net0["tap"] != "new-tap0" {
		t.Errorf("tap: got %v", net0["tap"])
	}
	if net0["mac"] != "11:22:33:44:55:66" {
		t.Errorf("mac: got %v", net0["mac"])
	}
	// JSON numbers are float64.
	if net0["num_queues"] != float64(8) {
		t.Errorf("num_queues: got %v", net0["num_queues"])
	}
	if net0["offload_tso"] != true {
		t.Errorf("offload_tso: got %v", net0["offload_tso"])
	}
	// Device id preserved.
	if net0["id"] != "_net0" {
		t.Errorf("id changed: got %v", net0["id"])
	}
}

func TestPatchCHConfig_SerialConsole(t *testing.T) {
	t.Run("direct_boot", func(t *testing.T) {
		dir := t.TempDir()
		path := writeCHConfig(t, dir, baseCHConfig())

		opts := basePatchOpts()
		opts.directBoot = true
		if err := patchCHConfig(path, opts); err != nil {
			t.Fatal(err)
		}

		result := readRawJSON(t, path)
		serial := result["serial"].(map[string]any)
		if serial["mode"] != "Off" {
			t.Errorf("serial.mode: got %v, want Off", serial["mode"])
		}
		console := result["console"].(map[string]any)
		if console["mode"] != "Pty" {
			t.Errorf("console.mode: got %v, want Pty", console["mode"])
		}
	})

	t.Run("uefi_boot", func(t *testing.T) {
		dir := t.TempDir()
		cfg := baseCHConfig()
		cfg["payload"] = map[string]any{"firmware": "/boot/OVMF.fd"}
		path := writeCHConfig(t, dir, cfg)

		opts := basePatchOpts()
		opts.directBoot = false
		opts.consoleSock = "/new/console.sock"
		if err := patchCHConfig(path, opts); err != nil {
			t.Fatal(err)
		}

		result := readRawJSON(t, path)
		serial := result["serial"].(map[string]any)
		if serial["mode"] != "Socket" {
			t.Errorf("serial.mode: got %v, want Socket", serial["mode"])
		}
		if serial["socket"] != "/new/console.sock" {
			t.Errorf("serial.socket: got %v", serial["socket"])
		}
		console := result["console"].(map[string]any)
		if console["mode"] != "Off" {
			t.Errorf("console.mode: got %v, want Off", console["mode"])
		}
	})
}

func TestPatchCHConfig_CPUMemoryBalloon(t *testing.T) {
	dir := t.TempDir()
	path := writeCHConfig(t, dir, baseCHConfig())

	opts := basePatchOpts()
	opts.cpu = 4
	opts.memory = 2 << 30 // 2 GiB
	if err := patchCHConfig(path, opts); err != nil {
		t.Fatal(err)
	}

	result := readRawJSON(t, path)

	cpus := result["cpus"].(map[string]any)
	if cpus["boot_vcpus"] != float64(4) {
		t.Errorf("boot_vcpus: got %v, want 4", cpus["boot_vcpus"])
	}
	// max_phys_bits preserved.
	if cpus["max_phys_bits"] != float64(46) {
		t.Errorf("max_phys_bits lost: got %v", cpus["max_phys_bits"])
	}

	mem := result["memory"].(map[string]any)
	if mem["size"] != float64(2<<30) {
		t.Errorf("memory.size: got %v, want %v", mem["size"], float64(2<<30))
	}
	// shared preserved.
	if _, ok := mem["shared"]; !ok {
		t.Error("memory.shared lost")
	}

	balloon := result["balloon"].(map[string]any)
	expectedSize := float64(2 << 30 / 4)
	if balloon["size"] != expectedSize {
		t.Errorf("balloon.size: got %v, want %v", balloon["size"], expectedSize)
	}
	// Balloon device id preserved.
	if balloon["id"] != "_balloon0" {
		t.Errorf("balloon.id lost: got %v", balloon["id"])
	}
}

func TestPatchCHConfig_BalloonRemoved(t *testing.T) {
	dir := t.TempDir()
	path := writeCHConfig(t, dir, baseCHConfig())

	opts := basePatchOpts()
	opts.memory = 128 << 20 // 128 MiB, below minBalloonMemory
	if err := patchCHConfig(path, opts); err != nil {
		t.Fatal(err)
	}

	result := readRawJSON(t, path)
	if _, ok := result["balloon"]; ok {
		t.Error("balloon should be removed for memory < 256 MiB")
	}
}

func TestPatchCHConfig_BalloonCreated(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCHConfig()
	delete(cfg, "balloon") // no balloon initially
	path := writeCHConfig(t, dir, cfg)

	opts := basePatchOpts()
	opts.memory = 1 << 30 // 1 GiB
	if err := patchCHConfig(path, opts); err != nil {
		t.Fatal(err)
	}

	result := readRawJSON(t, path)
	balloon, ok := result["balloon"].(map[string]any)
	if !ok {
		t.Fatal("balloon should be created for memory >= 256 MiB")
	}
	expectedSize := float64(1 << 30 / 4)
	if balloon["size"] != expectedSize {
		t.Errorf("balloon.size: got %v, want %v", balloon["size"], expectedSize)
	}
	if balloon["deflate_on_oom"] != true {
		t.Error("deflate_on_oom should be true")
	}
}

func TestPatchCHConfig_DiskCountMismatch(t *testing.T) {
	dir := t.TempDir()
	path := writeCHConfig(t, dir, baseCHConfig())

	opts := basePatchOpts()
	// 3 storage configs vs 2 disks in config.
	opts.storageConfigs = append(opts.storageConfigs, &types.StorageConfig{Path: "/extra"})
	err := patchCHConfig(path, opts)
	if err == nil {
		t.Fatal("expected error for disk count mismatch")
	}
	if !strings.Contains(err.Error(), "disk count mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPatchCHConfig_NetCountMismatch(t *testing.T) {
	dir := t.TempDir()
	path := writeCHConfig(t, dir, baseCHConfig())

	opts := basePatchOpts()
	// 2 network configs vs 1 net in config.
	opts.networkConfigs = append(opts.networkConfigs, &types.NetworkConfig{Tap: "extra"})
	err := patchCHConfig(path, opts)
	if err == nil {
		t.Fatal("expected error for net count mismatch")
	}
	if !strings.Contains(err.Error(), "net count mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

// updateCOWPath

func TestUpdateCOWPath_DirectBoot(t *testing.T) {
	configs := []*types.StorageConfig{
		{Path: "/old/layer.erofs", RO: true, Serial: "layer0"},
		{Path: "/old/cow.raw", RO: false, Serial: CowSerial},
	}
	if err := updateCOWPath(configs, "/new/cow.raw", true); err != nil {
		t.Fatal(err)
	}
	if configs[0].Path != "/old/layer.erofs" {
		t.Errorf("RO layer path changed: %s", configs[0].Path)
	}
	if configs[1].Path != "/new/cow.raw" {
		t.Errorf("COW path not updated: %s", configs[1].Path)
	}
}

func TestUpdateCOWPath_DirectBoot_NoMatch(t *testing.T) {
	configs := []*types.StorageConfig{
		{Path: "/old/layer.erofs", RO: true, Serial: "layer0"},
		{Path: "/old/data.raw", RO: false, Serial: "other-serial"},
	}
	err := updateCOWPath(configs, "/new/cow.raw", true)
	if err == nil {
		t.Fatal("expected error when no COW serial matches")
	}
	if !strings.Contains(err.Error(), CowSerial) {
		t.Errorf("error should mention serial %q: %v", CowSerial, err)
	}
}

func TestUpdateCOWPath_NonDirectBoot(t *testing.T) {
	configs := []*types.StorageConfig{
		{Path: "/old/base.qcow2", RO: true},
		{Path: "/old/overlay.qcow2", RO: false},
	}
	if err := updateCOWPath(configs, "/new/overlay.qcow2", false); err != nil {
		t.Fatal(err)
	}
	if configs[0].Path != "/old/base.qcow2" {
		t.Errorf("RO path changed: %s", configs[0].Path)
	}
	if configs[1].Path != "/new/overlay.qcow2" {
		t.Errorf("writable path not updated: %s", configs[1].Path)
	}
}

// rebuildStorageConfigs

func TestRebuildStorageConfigs(t *testing.T) {
	cfg := &chVMConfig{
		Disks: []chDisk{
			{Path: "/a.erofs", ReadOnly: true, Serial: "s1", NumQueues: 4, ImageType: "Raw"},
			{Path: "/b.raw", ReadOnly: false, Serial: "s2", Sparse: true},
			{Path: "/c.qcow2", ReadOnly: false, ImageType: "Qcow2"},
		},
	}

	result := rebuildStorageConfigs(cfg)
	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}

	tests := []struct {
		path, serial string
		ro           bool
	}{
		{"/a.erofs", "s1", true},
		{"/b.raw", "s2", false},
		{"/c.qcow2", "", false},
	}
	for i, tt := range tests {
		if result[i].Path != tt.path {
			t.Errorf("[%d] Path: got %q, want %q", i, result[i].Path, tt.path)
		}
		if result[i].RO != tt.ro {
			t.Errorf("[%d] RO: got %v, want %v", i, result[i].RO, tt.ro)
		}
		if result[i].Serial != tt.serial {
			t.Errorf("[%d] Serial: got %q, want %q", i, result[i].Serial, tt.serial)
		}
	}
}

// rebuildBootConfig

func TestRebuildBootConfig(t *testing.T) {
	t.Run("nil_payload", func(t *testing.T) {
		cfg := &chVMConfig{}
		if boot := rebuildBootConfig(cfg); boot != nil {
			t.Errorf("expected nil, got %+v", boot)
		}
	})

	t.Run("kernel_initramfs", func(t *testing.T) {
		cfg := &chVMConfig{Payload: &chPayload{
			Kernel: "/vmlinux", Initramfs: "/initrd", Cmdline: "console=hvc0",
		}}
		boot := rebuildBootConfig(cfg)
		if boot == nil {
			t.Fatal("expected non-nil")
		}
		if boot.KernelPath != "/vmlinux" {
			t.Errorf("KernelPath: %q", boot.KernelPath)
		}
		if boot.InitrdPath != "/initrd" {
			t.Errorf("InitrdPath: %q", boot.InitrdPath)
		}
		if boot.Cmdline != "console=hvc0" {
			t.Errorf("Cmdline: %q", boot.Cmdline)
		}
	})

	t.Run("firmware", func(t *testing.T) {
		cfg := &chVMConfig{Payload: &chPayload{Firmware: "/OVMF.fd"}}
		boot := rebuildBootConfig(cfg)
		if boot == nil {
			t.Fatal("expected non-nil")
		}
		if boot.FirmwarePath != "/OVMF.fd" {
			t.Errorf("FirmwarePath: %q", boot.FirmwarePath)
		}
	})

	t.Run("empty_payload", func(t *testing.T) {
		cfg := &chVMConfig{Payload: &chPayload{}}
		if boot := rebuildBootConfig(cfg); boot != nil {
			t.Errorf("expected nil for empty payload, got %+v", boot)
		}
	})
}

// patchStateJSON

func TestPatchStateJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	original := `{"disk_path":"/old/vm1/cow.raw","other":"/old/vm1/layer.erofs","nested":{"path":"/old/vm1/cow.raw"}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	diskPathMap := map[string]string{
		"/old/vm1/cow.raw":    "/new/vm2/cow.raw",
		"/old/vm1/layer.erofs": "/new/vm2/layer.erofs",
	}
	if err := patchStateJSON(path, diskPathMap); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "/old/vm1/") {
		t.Errorf("old paths still present: %s", content)
	}
	if !strings.Contains(content, "/new/vm2/cow.raw") {
		t.Errorf("new cow path missing: %s", content)
	}
	if !strings.Contains(content, "/new/vm2/layer.erofs") {
		t.Errorf("new layer path missing: %s", content)
	}
}

func TestPatchStateJSON_MACAddresses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// CH's serde serializes MAC as decimal byte array: aa:bb:cc:dd:ee:f0 → 170,187,204,221,238,240
	original := `{"disk_path":"/old/vm1/cow.raw","virtio_net":{"mac":[170,187,204,221,238,240],"queues":4}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Replacement map uses serde byte format (as buildStateReplacements produces).
	// 11:22:33:44:55:66 → 17,34,51,68,85,102
	replacements := map[string]string{
		"/old/vm1/cow.raw":          "/new/vm2/cow.raw",
		"170,187,204,221,238,240": "17,34,51,68,85,102",
	}
	if err := patchStateJSON(path, replacements); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// Old MAC bytes should be gone.
	if strings.Contains(content, "170,187,204,221,238,240") {
		t.Errorf("old MAC bytes still present: %s", content)
	}
	// New MAC bytes should appear.
	if !strings.Contains(content, "17,34,51,68,85,102") {
		t.Errorf("new MAC bytes missing: %s", content)
	}
	// Disk path also replaced.
	if strings.Contains(content, "/old/vm1/") {
		t.Errorf("old disk path still present: %s", content)
	}
	if !strings.Contains(content, "/new/vm2/cow.raw") {
		t.Errorf("new disk path missing: %s", content)
	}
}

func TestMacToSerdeBytes(t *testing.T) {
	tests := []struct {
		mac  string
		want string
	}{
		{"4e:08:ba:c1:62:f8", "78,8,186,193,98,248"},
		{"fe:86:bf:1f:53:17", "254,134,191,31,83,23"},
		{"00:00:00:00:00:00", "0,0,0,0,0,0"},
		{"ff:ff:ff:ff:ff:ff", "255,255,255,255,255,255"},
	}
	for _, tt := range tests {
		got, err := macToSerdeBytes(tt.mac)
		if err != nil {
			t.Errorf("macToSerdeBytes(%q): %v", tt.mac, err)
			continue
		}
		if got != tt.want {
			t.Errorf("macToSerdeBytes(%q) = %q, want %q", tt.mac, got, tt.want)
		}
	}
}

func TestBuildStateReplacements(t *testing.T) {
	chCfg := &chVMConfig{
		Disks: []chDisk{
			{Path: "/old/layer.erofs"},
			{Path: "/old/cow.raw"},
		},
		Nets: []chNet{
			{Mac: "4e:08:ba:c1:62:f8"},
		},
	}
	storageConfigs := []*types.StorageConfig{
		{Path: "/old/layer.erofs"}, // unchanged
		{Path: "/new/cow.raw"},     // changed
	}
	networkConfigs := []*types.NetworkConfig{
		{Mac: "fe:86:bf:1f:53:17"}, // changed
	}

	m := buildStateReplacements(chCfg, storageConfigs, networkConfigs)

	// Disk path: only changed one.
	if m["/old/cow.raw"] != "/new/cow.raw" {
		t.Errorf("disk replacement missing or wrong: %v", m)
	}
	if _, ok := m["/old/layer.erofs"]; ok {
		t.Error("unchanged disk path should not be in replacements")
	}

	// MAC: serde byte format.
	oldMAC := "78,8,186,193,98,248"   // 4e:08:ba:c1:62:f8
	newMAC := "254,134,191,31,83,23"  // fe:86:bf:1f:53:17
	if m[oldMAC] != newMAC {
		t.Errorf("MAC replacement: got %q→%q, want %q→%q", oldMAC, m[oldMAC], oldMAC, newMAC)
	}
}

func TestPatchStateJSON_EmptyMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	original := `{"disk_path":"/some/path"}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchStateJSON(path, map[string]string{}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != original {
		t.Errorf("file changed with empty map: %s", data)
	}
}
