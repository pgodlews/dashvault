### Building the Dashvault SD image

The image is Raspberry Pi OS Lite (Debian Trixie, 32-bit, so one image runs on
every supported Raspberry Pi) with a first-boot script, a sample configuration
file and, normally, a complete Dashvault release. With the release in the
image, first boot installs exactly that release and downloads no scripts from
GitHub. It still needs internet access for a few Debian packages.

1. Build the release archives from the repository root:

   ```
   tools/build-release.sh v1.2.3
   ```

   This writes `dist/release/teslausb-release.tar.gz` (for the image) and
   `dist/release/teslausb-update.tar.gz` (the app update for installed boards).
2. Clone pi-gen from https://github.com/RPi-Distro/pi-gen (the `master` branch
   builds Trixie) and install Docker.
3. From the pi-gen folder, run `prepare.sh` from this folder with the release
   archive as its argument:

   ```
   /path/to/pi-gen-sources/prepare.sh /path/to/dist/release/teslausb-release.tar.gz
   ```

   Without the argument you get a "thin" image that downloads its scripts from
   GitHub at first boot, as teslausb's image does.
4. Run `./build-docker.sh`. On an x86 machine the build registers an ARM
   emulator with the kernel for its duration, so it needs a privileged
   container. It takes about 20 minutes on a 16-core machine.
5. The image is in `deploy/`. Flash it with Raspberry Pi Imager ("Use custom").

`prepare.sh` makes three changes to pi-gen: it adds the Dashvault stage, removes
the cloud-init stage (first boot is driven by `rc.local`), and gives the root
partition about 2.5 GB of free space for setup and for later operating system
updates. The root partition is deliberately not grown on first boot, because
setup uses the rest of the card for the dashcam drive.

### Publishing a release

1. Push a `vX.Y.Z` tag. The Release workflow builds the archives and creates a
   draft, which installed boards and the installer cannot see.
2. Download the draft's `teslausb-release.tar.gz` and build the image from it
   as described above.
3. On the machine with the offline signing key, run:

   ```
   tools/sign-release.sh vX.Y.Z /path/to/deploy/image_DATE-dashvault.zip
   ```

   It checks the draft, adds the image to `SHA256SUMS`, signs that file and
   uploads the image and signature.
4. Publish the draft with the command the script prints. Do not mark it as a
   pre-release, because the installer and in-app updates follow
   `releases/latest`, which ignores pre-releases.
