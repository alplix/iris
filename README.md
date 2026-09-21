# Iris

**Iris** is a volunteer-computing grid in one package: a lightweight, BOINC-compatible compute
**client** (`irisd`) plus a native desktop **manager** (`iris`) for every machine in your fleet.

- **Iris client (`irisd`)** — downloads work from BOINC-compatible project servers, runs it on your
  CPU/GPU, and reports results back. Pure Go, zero external dependencies, single static binary.
- **Iris manager (`iris`)** — monitors and controls many clients (local and remote) from one window:
  tasks, projects, transfers, preferences, statistics, notifications.

Built with **Go + [Wails v2](https://wails.io)** for the desktop UI. Native WebView on every
platform — no browser, no Electron. The manager speaks the BOINC **GUI RPC** protocol, so it can
manage `irisd` clients and other BOINC-compatible clients alike.

## Features

| Area | What you can do |
|---|---|
| **Compute client** | Attach to projects, fetch work, run tasks in a slot sandbox, upload results, GPU/CPU resource control |
| **Fleet dashboard** | Live totals (running / paused / queued), RAC & credit, per-host activity sparkline, recent client messages |
| **Tasks** | Progress, elapsed/CPU time, ETA, deadlines, GPU/CPU resources; pause / resume / abort per task; filters + search |
| **Projects** | Attach (with email/password authenticator lookup), detach, suspend/resume, update, no-more-work / allow-work |
| **Transfers** | Live upload/download progress with retry & abort |
| **Messages** | Severity-highlighted client log across all servers |
| **Statistics** | Per-project credit-history charts (365 days), transfer history, per-project disk usage |
| **Preferences** | Global-pref overrides stored on the client (`max_ncpus_pct`/`max_ncpus` take effect immediately), per-host & **fleet-wide** run/network modes, CPU benchmark |
| **Hardware** | OS, CPU, cores, FLOPS, RAM, disk, GPU list with VRAM per host |
| **Notifications** | Desktop alerts for deadlines, task errors and offline hosts; tray menu with refresh/hide/show |
| **Languages** | English, Türkçe, Deutsch, Français, Español, Italiano, Português, Русский, 日本語 — picked from the system language, changeable in Settings |
| **Local client** | Auto-detect (and start/stop) the bundled `irisd` client right from Settings |

## Platforms

| OS | Architectures | Packaging |
|---|---|---|
| Windows 10/11 | amd64, arm64 | NSIS installer + portable zip (WebView2 automatic) |
| macOS | amd64, arm64 | `.app` bundle (zip) |
| Linux | amd64, arm64 | portable `.tar.gz` (GTK3 + WebKitGTK 4.1 required) |

Every release bundles `irisd` alongside the manager. The manager auto-detects it (bundled copy
first, then the installed location) and registers it as **Local Iris**.

### Client-only platforms (32-bit, RISC-V, POWER)

The compute client `irisd` is pure Go with no native dependencies, so it also ships on its own for
machines that cannot run the desktop manager. Manage them from the manager on any other computer
(**Servers → Add Server**, port `31418`).

| Archive | For |
|---|---|
| `irisd-windows-386.zip` | 32-bit Windows |
| `irisd-linux-386.tar.gz` | 32-bit x86 Linux |
| `irisd-linux-armv7.tar.gz` | 32-bit ARM: Raspberry Pi 2/3/4/5 running a 32-bit OS, most ARM boards |
| `irisd-linux-armv6.tar.gz` | Raspberry Pi 1 / Zero |
| `irisd-linux-riscv64.tar.gz` | RISC-V (64-bit) boards and servers |
| `irisd-linux-ppc64le.tar.gz` | POWER8 and newer (little-endian) |
| `irisd-linux-ppc64.tar.gz` | big-endian 64-bit PowerPC / POWER |

The client's tests run on all of these in CI (through QEMU where the runner is x86-64). Two limits:

- **Work availability is up to each project.** A project only sends tasks for platforms it has
  applications for, and many projects have none for 32-bit ARM, RISC-V or POWER. Iris reports the
  standard BOINC platform name of the machine (`riscv64-unknown-linux-gnu`,
  `powerpc64le-unknown-linux-gnu`, `arm-unknown-linux-gnueabihf`, `i686-pc-linux-gnu`, ...).
- **32-bit PowerPC** (G3/G4/G5-era Macs and boards) is not supported: the Go toolchain has no
  32-bit PowerPC port.

On boards and other CPUs Iris does not recognise it measures its own speed at startup instead of
guessing, so a slow machine is not handed more work than it can finish.

## Install

Download the latest release from the [Releases page](https://github.com/alplix/iris/releases).

- **Windows** — run `iris-amd64-installer.exe` (or `iris-arm64-installer.exe` on ARM PCs). It installs
  the manager **and** the `irisd` client to `Program Files\alplix\Iris`, adds Start-menu and desktop
  shortcuts, offers to launch Iris when it finishes, and can be removed from *Apps & features*. The
  installer speaks the same nine languages as the app and picks the one matching your Windows
  language. Prefer no installer? Use the portable `iris-windows-*.zip`.
- **macOS** — unzip `iris-darwin-*.zip` and move `Iris.app` to Applications.
- **Linux** — extract `iris-linux-*.tar.gz` (GTK3 + WebKitGTK 4.1 required).

## Getting started

### 1. Run the client

```
irisd            # run the compute client (foreground)
irisd --daemon   # same, detached (output goes to irisd.log in the data directory)
irisd --status   # show client status
irisd --stop     # stop the running client
```

The client stores its data in `%ProgramData%\Iris` (Windows), `~/Library/Application Support/Iris`
(macOS) or `~/.local/share/iris` (Linux): `client_state.xml`, `cc_config.xml`, `gui_rpc_auth.cfg`,
project slots and the result cache. It listens for management connections on **port 31418**
(override with `IRIS_GUI_RPC_PORT`).

> Default is `31418`, not BOINC's `31416`, so an Iris client and a stock BOINC client can coexist
> on the same machine.

Every GUI RPC connection has to authenticate: the first run generates a random password into
`gui_rpc_auth.cfg` (owner-readable only), and the client refuses any command before the
challenge–response handshake succeeds. Set `allow_remote_gui_rpc` to `0` in `cc_config.xml` to
listen on `127.0.0.1` only.

### 2. Manage with the GUI

1. Launch **Iris** — the manager auto-detects the local client, or you can connect to remote hosts.
2. For remote clients, find the RPC password in `gui_rpc_auth.cfg` inside the client data directory.
3. Open **Servers → Add Server** and enter host, port (default `31418`) and password.
4. Iris starts clean, like the BOINC manager: on first launch it finds the bundled `irisd`, adds it as
   **Local Iris** and starts it for you (unless you stopped it yourself). Nothing else is added.
   Developers can get a simulated server with `IRIS_DEMO=1`.

Configuration is stored in the OS config directory (`~/.config/iris` on Linux,
`%APPDATA%\iris` on Windows, `~/Library/Application Support/iris` on macOS). Host passwords live
in `hosts.json` (0600).

### 3. Attach a project

Use the manager's **Projects → Attach** (project master URL + email/password). The manager sends
the attach request over GUI RPC; the client contacts the project scheduler, joins, downloads work
and starts computing automatically.

## Running next to BOINC

Iris is built to coexist with a stock BOINC client on the same machine. Nothing is shared:

| | Iris | BOINC |
|---|---|---|
| GUI RPC port | `31418` (`IRIS_GUI_RPC_PORT`; `31416` is refused) | `31416` |
| Client data | `%ProgramData%\Iris` / `~/.local/share/iris` / `~/Library/Application Support/Iris` | `%ProgramData%\BOINC` / `/var/lib/boinc-client` / `/Library/Application Support/BOINC Data` |
| Manager settings | `%APPDATA%\iris` / `~/.config/iris` | BOINC Manager's own |
| Programs | `iris`, `irisd` | `boincmgr`, `boinc` |
| Host identity on project servers | random ID stored in `host_cpid.txt`, per installation | BOINC's own |

Each client registers as its own host on a project, and no hardware identifier is sent. The one
thing the two cannot avoid sharing is the hardware: running both means both compute, so lower
`max_ncpus_pct` in Iris' preferences (or set one client to *never*) if you want to split the CPU.
The manager can also monitor BOINC clients — add them as a server on port `31416`.

## Building from source

Prerequisites:

- Go 1.26+
- [Wails CLI](https://wails.io/docs/gettingstarted/installation) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- Node.js 20+ (frontend tooling)

On Linux you also need the [Wails WebKit/GTK dependencies](https://wails.io/docs/guides/linux)
(`libgtk-3-dev libwebkit2gtk-4.1-dev libayatana-appindicator3-dev`). On Windows, **WebView2** is
required at runtime (bundled by the installer).

```
wails dev        # live development build of the manager
wails build      # production build of the manager for the current platform
                 # (Linux with WebKitGTK 4.1: wails build -tags webkit2_41)
go build ./cmd/irisd   # build just the compute client
```

Cross-compiling for release targets is handled by CI:

- `.github/workflows/ci.yml` — vet, tests, frontend build and a Linux smoke build on every PR/push
- `.github/workflows/release.yml` — tagged releases (`vX.Y.Z`) for Windows, macOS and Linux with
  checksums; trigger with `git tag v1.0.0 && git push origin v1.0.0`

## Architecture

```
cmd/irisd/                Iris compute client (the daemon)
cmd/iris/, main.go        Iris manager (Wails app entrypoints)
app.go                    Wails-bound API surface (main namespace)

frontend/                 Vite + vanilla JS/CSS single-page UI
  src/main.js             state, rendering, Wails bindings, notifications
  wailsjs/                generated TS bindings to the Go API

internal/                 shared Go packages
  app/                    manager logic (unit-tested): snapshots, polling, host ops
  boinc/                  BOINC GUI-RPC client (xmlrpc-over-TCP)
  cache/                  result cache (CPU/GPU, separate slots & projects)
  config/                 cc_config handling + data directory resolution
  detect/                 host hardware/GPU/CPU probe
  guirpc/                 GUI-RPC server the manager talks to (per-connection authentication)
  i18n/                   embedded UI translations, served to the frontend as `GetTranslations`
  local/                  local daemon detection & lifecycle management
  prefs/                  global_prefs_override.xml store and the limits derived from it
  product/                shared product identity (name, version, port)
  project/                project (account) management
  scheduler/              BOINC project scheduler client + engine
  state/                  client_state.xml model
  worker/                 task slot runner
  xml/                    XML helpers
```

The manager's Go API is bound under `window.go.main.App` and talks to the UI through plain method
calls plus a single event channel (`notice`) for deadline/error/offline notifications.

## Release checklist

1. Bump `product.Version` in `internal/product/product.go` and `productVersion` in `wails.json`.
2. `git tag v1.0.0 && git push origin v1.0.0`
3. Download the release assets; each job uploads `SHA256SUMS.txt` alongside the archives.

## Roadmap

- .deb / .rpm / AppImage packaging for Linux
- Automatic `irisd` first-run install flow inside the manager

## License

MIT — see [LICENSE](LICENSE). The manager works with BOINC-compatible servers; buy-in from a
project scheduler is required to receive work.