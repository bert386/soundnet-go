# Development and deployment environment

Operational facts for this fork. Records things that were discovered the hard way.

## Development machine (Windows 11)

| | |
|---|---|
| Repo (authoritative) | `/root/soundnet-go` inside WSL2 Ubuntu 26.04 |
| Windows access | `\wsl.localhost\Ubuntu\root\soundnet-go` |
| Toolchain | Go 1.27.0, Node 24.21.0, Task 3.53.1, ONNX Runtime 1.25.1 |
| WSL memory | 4GB + 8GB swap, set in `C:\Users\arnol\.wslconfig` |

**The repo lives on ext4, not `/mnt/c`, deliberately.** Measured: `/mnt/c` is about
30x slower on small-file writes and 59x on reads (9p filesystem). A Go build plus
`npm install` touches tens of thousands of files, so building from `/mnt/c` is not viable.

**Build and test only through `task`, never bare `go build` / `go test`.** The Taskfile
supplies `CGO_CFLAGS=-I.cache/tensorflow`, without which every cgo package fails with
`tensorflow/lite/c/c_api.h: No such file or directory` even though the headers are present.
Tests additionally need `-tags noembed,skipfrontend`, or packages embedding `frontend/dist`
fail to build.

Measured: build ~5s warm, full `task test` ~150s.

## Known environment quirks

- **WSL runs as root.** Consequence: 8 tests fail that assert permission denials, because
  root bypasses them (`TestCheckWritePermission_FailsOnReadOnlyDir`,
  `TestUnwritableTokensDirectory`, `TestOpen_ClosesPoolOnPostConnectionFailure`,
  `TestElevateImport_InsufficientSpace`). Verified they pass as an unprivileged user.
  A `devtest` user exists for re-checking.
- **`TestCollectDeploymentInfo_Orchestration` fails under WSL** regardless of user: it reads
  WSL's own mount table (`/mnt/wsl`, `/mnt/c` over 9p, overlays) and classifies them as
  Docker mounts. Environmental, not a fork defect.
- **Pushing to GitHub does not work from WSL.** Git Credential Manager cannot present a
  prompt and hangs indefinitely with no error. Push from the Windows checkout instead:

      cd C:\Users\arnol\Projects\SoundNet\git\soundnet-go
      git fetch "//wsl.localhost/Ubuntu/root/soundnet-go" main
      git push origin FETCH_HEAD:refs/heads/main

  That checkout is a **push relay only** - never edit code there.
- **SSH from WSL to the Pi hangs** at the handshake though the TCP connect succeeds. Not MTU
  (tested down to 1200). Use Windows OpenSSH for anything involving the Pi.

## Deployment target

| | |
|---|---|
| Host | `pi@192.168.3.89` (hostname `birdpi`) |
| Hardware | Raspberry Pi 4 Model B Rev 1.5, 4 cores, 7.6GB RAM |
| OS | Debian 13 (trixie), arm64 / aarch64, kernel 6.18.50 |
| Disk | 24GB free of 29GB |
| Audio capture | `card 1: KT USB Audio` - USB microphone already attached |
| Access | key-based, `C:\Users\arnol\.ssh\soundnet_pi`, no password |

Cross-compilation to `linux/arm64` is installed on the dev machine, so the Pi does not need
a Go toolchain; build locally and copy the binary.

This is the hardware the M3 performance budget (<100ms per clip) is measured against.
It is also where M5/M6 will run against the live ADS-B feed, so the station coordinates in
the web config refer to *this* machine's location, not the development machine's.

## Keeping the service alive on the Pi

`nohup`, `setsid` and `systemd-run --user` all fail: the process is SIGTERMed
when the SSH session ends, because `Linger=no` for the `pi` user and there is no
tmux or screen installed. Enabling lingering needs root.

A **cron keepalive** works, because cron runs detached from login sessions:

    ~/soundnet/keepalive.sh      starts the binary if it is not running
    crontab:  * * * * *  and  @reboot

## Deploying a new build

The binary is ~173 MB and a single `scp` stalls part-way on this wireless link -
`scp` stays alive while transferring nothing, the signature of an idle or NAT
timeout. What works:

1. `gzip -9` the binary (~107 MB) and `split -b 20m` it
2. `scp` each chunk with `-o ServerAliveInterval=10`, retrying per chunk
3. reassemble and `gunzip` on the Pi

**Pause the cron keepalive before swapping the binary.** Otherwise cron restarts
the old process during the swap and `cp`/`mv` fails with "text file busy" - and
with `set -e` the script aborts silently, leaving the old binary in place and
looking like success. **Verify by md5 afterwards, not by checking the port.**

Consider building with the `noembed` tag for iteration: most of the 173 MB is the
embedded frontend and models.
