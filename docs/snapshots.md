# Snapshots: how RecentClips are kept on the Pi

The car records to `cam_disk.bin`, a FAT/exFAT image the Pi presents as a USB
drive. The car keeps only its own rolling buffer of RecentClips (about the last
hour) and reuses the space after that. Snapshots are how the Pi keeps more:
every `SNAPSHOT_INTERVAL` it takes a copy-on-write copy of the whole image, so
clips the car has since deleted are still readable from older snapshots until
free space runs low.

Code: `run/archiveloop` (`snapshotloop`, `freespacemanager`),
`run/make_snapshot.sh`, `run/waitforidle`, `run/auto.teslausb`,
`run/manage_free_space.sh`, `run/release_snapshot.sh`. Paths come from
`run/lib/paths.sh`.

## The pieces

```mermaid
flowchart LR
    Car[Tesla] -- USB mass storage --> Disk[("/backingfiles/cam_disk.bin<br/>the drive the car writes")]

    subgraph bg["archiveloop (background loops)"]
        SL["snapshotloop<br/>every SNAPSHOT_INTERVAL"]
        FS["freespacemanager<br/>every 30 s"]
    end

    SL -- "waitforidle, then make_snapshot.sh" --> Snap
    Disk -.->|"cp --reflink=always<br/>(shares blocks, no data copied)"| Snap

    subgraph snaps["/backingfiles/snapshots/"]
        Snap[("snap-000123/snap.bin<br/>+ snap.bin.toc")]
        Old[("snap-000122 ...<br/>older snapshots")]
    end

    Snap -- "autofs, read-only<br/>/tmp/snapshots/snap-000123" --> Links
    Old -- autofs --> Links

    Links["/mutable/TeslaCam/<br/>RecentClips/&lt;date&gt;/ · SavedClips/ · SentryClips/<br/>symlinks into the snapshots"]

    Links --> UI[Web UI viewer]
    Links --> Tel[telemetry_index.py]
    Links --> Arch[archive cycle → NAS]

    FS -- "low space: release oldest" --> Old
```

- **Snapshot image:** `snap.bin` is a reflink copy of `cam_disk.bin`. Taking one
  copies no video; the two files share every block until the car overwrites one.
- **Mounting:** snapshots are mounted read-only on demand by autofs
  (`/etc/auto.master.d/teslausb.autofs` → `run/auto.teslausb`), using the
  filesystem type and partition offset from `run/mountoptsforimage`.
- **Links:** `/mutable/TeslaCam` holds only symlinks. Each clip links to the
  newest snapshot that has it (`ln -sf` replaces older links), so one tree shows
  everything still on the Pi, regardless of which snapshot holds it.

## Taking a snapshot

```mermaid
sequenceDiagram
    autonumber
    participant SL as snapshotloop
    participant W as waitforidle
    participant MS as make_snapshot.sh
    participant D as cam_disk.bin
    participant S as snap-N/snap.bin
    participant AF as autofs
    participant L as /mutable/TeslaCam links

    SL->>SL: sleep SNAPSHOT_INTERVAL (default 480 s, min 60)
    SL->>W: wait for a pause in the car's writes
    Note over W: watches the USB gadget's write_bytes,<br/>gives up after 90 s and snapshots anyway
    W-->>SL: idle (or timed out)
    SL->>MS: run (holds flock on the snapshots dir)
    MS->>MS: previous snapshot has no .toc? delete it as incomplete
    MS->>S: cp --reflink=always cam_disk.bin
    D-->>S: blocks shared, nothing copied
    MS->>S: losetup -P + fsck -p (repairs the copy, never the car's drive)
    opt RECENTCLIPS_PRUNE_SNAPSHOTS=true (experimental)
        MS->>S: mount read-write, delete other cameras' RecentClips, fstrim
    end
    MS->>AF: wait for autofs, list the new mount
    AF->>S: mount read-only at /tmp/snapshots/snap-N
    MS->>MS: write file list (snap.bin.toc_) and diff with the previous .toc
    alt new or changed files
        MS->>L: symlink RecentClips/date, SavedClips, SentryClips, TrackMode files
        MS->>MS: rename .toc_ to .toc (snapshot complete)
    else identical to the previous snapshot
        MS->>S: release_snapshot.sh, delete snap-N
    end
```

Snapshots are also taken once at boot (`make_snapshot.sh nofsck`, after
`fix_errors_in_images` already checked the image) and right before each archive
cycle, so the archive sees the latest clips.

A snapshot only counts once its `.toc` exists. If power is lost anywhere before
that, the next run deletes the half-made snapshot; the clips are still in the
previous snapshots and on the car's drive.

## Why snapshots cost little space

```mermaid
flowchart TB
    subgraph t1["Just after snap-N is taken"]
        D1["cam_disk.bin"] --> B1["blocks of clips A, B, C"]
        S1["snap-N"] --> B1
    end
    subgraph t2["An hour later: the car deleted A and recorded D in its space"]
        D2["cam_disk.bin"] --> B2["blocks of B, C, D"]
        S2["snap-N"] --> B3["blocks of A<br/>(now only snap-N holds them)"]
        S2 --> B2s["blocks of B, C<br/>(still shared)"]
        D2 --> B2s
    end
    t1 --> t2
```

A snapshot only uses space for blocks the car has overwritten since. The space
in use is therefore about "how much video the car has recorded and deleted since
the oldest snapshot", which is what `freespacemanager` keeps in check.

## Free space

```mermaid
flowchart TD
    A["freespacemanager, every 30 s"] --> B{"free space on /backingfiles<br/>&lt; 10 GB + 3 % of the drive?"}
    B -- no --> A
    B -- yes --> C["manage_free_space.sh"]
    C --> D{"at least 2 snapshots?"}
    D -- no --> E["log a warning:<br/>use larger storage or reduce CAM_SIZE"]
    D -- yes --> F["release_snapshot.sh oldest<br/>unmount, delete snap-*, delete its links,<br/>remove empty folders"]
    F --> B
```

The oldest snapshot is deleted whole. How far back RecentClips go therefore
depends on storage size and how much the car records per hour.

## A clip's life

```mermaid
stateDiagram-v2
    [*] --> OnCarDrive: car writes a 1-minute file per camera
    OnCarDrive --> InSnapshots: next snapshot (within SNAPSHOT_INTERVAL)
    InSnapshots --> SnapshotsOnly: car's rolling buffer deletes it (~1 h)
    SnapshotsOnly --> Gone: every snapshot holding it was released (low space)

    OnCarDrive --> Saved: steering-wheel save / honk / Sentry event
    Saved --> InSnapshots: next snapshot links it under SavedClips or SentryClips

    InSnapshots --> Archived: archive cycle copies it (if its category is enabled)
    SnapshotsOnly --> Archived: archive cycle copies it
```

- **Saving with the steering-wheel button** happens on the car's drive: the car
  puts the last minutes of RecentClips into `SavedClips/<time>/`. The next
  snapshot sees that folder with every camera. Nothing the Pi does to snapshots
  changes the car's drive.
- **After archiving**, `clean_cam_mount freespace` may delete the oldest files
  from the car's drive to keep about 20 GB free for the car. Snapshots taken
  before that still hold them.

## The archive cycle and snapshots

```mermaid
sequenceDiagram
    participant AL as archiveloop
    participant MS as make_snapshot.sh
    participant OV as overlay /tmp/cam/merged
    participant IX as telemetry_index.py
    participant BE as archive-clips.sh (backend)
    participant D as car's drive

    AL->>MS: take a snapshot so the newest clips are included
    AL->>OV: overlay over /mutable/TeslaCam (links stay untouched)
    AL->>AL: list Saved/Sentry/TrackMode/Recent links per settings
    AL->>AL: RecentClips: drop cameras not in RECENTCLIPS_CAMERAS
    AL->>BE: copy to the NAS, one pass per category:<br/>SavedClips, SentryClips, TeslaTrackMode, RecentClips
    BE-->>OV: copied files are removed from the overlay only
    AL->>IX: index telemetry (front camera, one run, after copying)
    AL->>IX: mark-listed (every clip archived so far)
    AL->>OV: unmount
    AL->>D: clean_cam_mount freespace (car's drive only)
```

Archiving reads from snapshots, never from the drive the car is writing, so the
car keeps recording throughout.

## Experimental: pruning cameras from snapshots

With `RECENTCLIPS_PRUNE_SNAPSHOTS=true` and a camera list in
`RECENTCLIPS_CAMERAS` (Settings → Archive → RecentClips cameras), every new
snapshot is pruned right after `fsck`, before it is mounted read-only or linked:

```mermaid
flowchart TD
    A["new snap.bin (reflink copy)"] --> B["mountimage read-write<br/>(temporary mount point)"]
    B --> C["delete TeslaCam/RecentClips files<br/>named YYYY-MM-DD_HH-MM-SS-&lt;camera&gt;.mp4<br/>for cameras not selected"]
    C --> D{"telemetry map on?"}
    D -- yes --> E["front is always kept"]
    D -- no --> F
    E --> F["fstrim: punch holes in snap.bin<br/>for the freed clusters"]
    F --> G["unmount; continue with autofs mount,<br/>.toc and links"]
```

- **What it changes:** once the car deletes a clip from its drive, only the kept
  cameras' blocks remain in snapshots, so the same space holds more minutes
  (about 4× with front only on a 4-camera car). Older, unpruned snapshots keep
  every camera until they age out.
- **What it never touches:** the car's drive, `SavedClips`, `SentryClips`,
  `TeslaTrackMode`, symlinks, files whose names are not dashcam clips, and
  cameras the code does not know.
- **Failure handling:** if the read-write mount fails, the snapshot is kept
  unpruned. If `fstrim` fails, the space is released when the snapshot is
  deleted. A power cut leaves the snapshot without `.toc`, so it is discarded.
- **Cost:** a few hundred deletions per snapshot plus one `fstrim`; the log line
  `pruning: removed N RecentClips file(s) ... in Xs` records how long it took.
