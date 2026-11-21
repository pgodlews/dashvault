package main

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeBootTree builds a sysfs/procfs tree like a Radxa Zero 3 booted from
// mmcblk1p3, with an SDIO WiFi on mmc2 and an empty mmcblk0 slot.
func fakeBootTree(t *testing.T, rootMajMin string) (sys, proc string) {
	t.Helper()
	tmp := t.TempDir()
	sys = filepath.Join(tmp, "sys")
	proc = filepath.Join(tmp, "proc")
	card := filepath.Join(sys, "devices/platform/fe2b0000.mmc/mmc_host/mmc1/mmc1:5048")
	disk := filepath.Join(card, "block/mmcblk1")
	for _, dir := range []string{disk + "/mmcblk1p3", filepath.Join(sys, "dev/block"), filepath.Join(sys, "block"), filepath.Join(proc, "self")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(card, "name"), "SD64G\n")
	write(filepath.Join(card, "manfid"), "0x000027\n")
	write(filepath.Join(card, "date"), "11/2019\n")
	write(filepath.Join(disk, "size"), "124735488\n")
	write(filepath.Join(disk, "stat"), "1 0 0 0 2 0 4096 0 0 0 0\n")
	write(filepath.Join(disk, "mmcblk1p3/partition"), "3\n")
	for link, target := range map[string]string{
		filepath.Join(sys, "dev/block/179:3"): disk + "/mmcblk1p3",
		filepath.Join(sys, "block/mmcblk1"):   disk,
		filepath.Join(disk, "device"):         card,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(proc, "self/mountinfo"),
		"22 1 "+rootMajMin+" / / ro,noatime shared:1 - ext4 /dev/mmcblk1p3 ro\n"+
			"30 22 179:4 / /backingfiles rw shared:2 - xfs /dev/mmcblk1p4 rw\n")
	return sys, proc
}

func TestDetectBootDiskFollowsRootMount(t *testing.T) {
	sys, proc := fakeBootTree(t, "179:3")
	if got := detectBootDisk(sys, proc); got != "mmcblk1" {
		t.Fatalf("detectBootDisk = %q, want mmcblk1", got)
	}
}

func TestDetectBootDiskFallsBackToFirstMMC(t *testing.T) {
	sys, proc := fakeBootTree(t, "8:1") // root on a disk sysfs does not list
	if got := detectBootDisk(sys, proc); got != "mmcblk1" {
		t.Fatalf("detectBootDisk = %q, want mmcblk1", got)
	}
	empty := t.TempDir()
	if got := detectBootDisk(empty, empty); got != "mmcblk0" {
		t.Fatalf("detectBootDisk with nothing = %q, want mmcblk0", got)
	}
}

func TestCardInfoAndWriteStatsUseBootDisk(t *testing.T) {
	redirectBenchmarkPaths(t)
	sys, proc := fakeBootTree(t, "179:3")
	origSys, origProc, origName := bootSysRoot, bootProcRoot, bootDiskName
	bootSysRoot, bootProcRoot = sys, proc
	bootDiskName = detectBootDisk(sys, proc)
	bootDiskOnce.Do(func() {}) // already detected
	mmcBlockStatPath = ""
	t.Cleanup(func() { bootSysRoot, bootProcRoot, bootDiskName = origSys, origProc, origName })

	info := readSdCardInfoSysfs()
	if !info.Available || info.Name != "SD64G" || info.Manufacturer != "Phison" || info.Date != "11/2019" {
		t.Fatalf("card info = %+v", info)
	}
	if info.CapacityGB < 63 || info.CapacityGB > 64 {
		t.Fatalf("capacity = %v GB, want about 63.9", info.CapacityGB)
	}

	UpdateWriteStats()
	if err := os.WriteFile(filepath.Join(sys, "block/mmcblk1/stat"), []byte("1 0 0 0 2 0 6144 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	UpdateWriteStats()
	stats := readWriteStats()
	if stats == nil || stats.TotalBytesWritten != 2048*512 {
		t.Fatalf("write stats = %+v, want %d bytes", stats, 2048*512)
	}
}
