package main

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The card holding the root filesystem is not always mmcblk0: a Raspberry Pi
// boots from mmcblk0, but a Radxa Zero 3 boots from mmcblk1 (its SD slot is
// mmc1, the WiFi mmc2). Find it from the root mount instead.
var (
	bootSysRoot  = "/sys"
	bootProcRoot = "/proc"

	bootDiskOnce sync.Once
	bootDiskName string
)

// bootDisk returns the block device name (e.g. "mmcblk1") of the disk that
// holds the root filesystem. Detected once; the boot disk cannot change.
func bootDisk() string {
	bootDiskOnce.Do(func() {
		bootDiskName = detectBootDisk(bootSysRoot, bootProcRoot)
	})
	return bootDiskName
}

// detectBootDisk finds the root mount's device number in mountinfo and
// resolves it through /sys/dev/block to its parent disk. Falls back to the
// first mmcblk disk, then to mmcblk0.
func detectBootDisk(sys, proc string) string {
	if majMin := rootMajMin(filepath.Join(proc, "self/mountinfo")); majMin != "" {
		devLink := filepath.Join(sys, "dev/block", majMin)
		if resolved, err := filepath.EvalSymlinks(devLink); err == nil {
			if _, err := os.Stat(filepath.Join(resolved, "partition")); err == nil {
				resolved = filepath.Dir(resolved)
			}
			if name := filepath.Base(resolved); name != "" && name != "." && name != "/" {
				return name
			}
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(sys, "block/mmcblk*")); len(matches) > 0 {
		sort.Strings(matches)
		return filepath.Base(matches[0])
	}
	return "mmcblk0"
}

// rootMajMin returns the "major:minor" of the filesystem mounted at "/".
func rootMajMin(mountinfo string) string {
	f, err := os.Open(mountinfo)
	if err != nil {
		return ""
	}
	defer f.Close()
	result := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// id parent major:minor root mountpoint ...
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 && fields[4] == "/" {
			result = fields[2] // the last mount on "/" is the one in use
		}
	}
	return result
}

// bootDiskSysfs returns /sys/block/<boot disk>/<name>.
func bootDiskSysfs(name string) string {
	return filepath.Join(bootSysRoot, "block", bootDisk(), name)
}
