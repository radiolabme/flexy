# Flexy

FlexRadio bridge providing hamlib/rigctld CAT control, a SmartSDR TCP+UDP proxy, and an embedded web UI — designed to run on a Linux machine co-located with your FlexRadio on its local network.

## What it does

- **Hamlib interface** (`:4532`) — exposes your Flex radio as a rigctld-compatible rig to logging software, WSJT-X, digital mode apps, etc.
- **SmartSDR proxy** (`:4992`) — allows SmartSDR, DAX, and CAT apps on remote machines to connect as if they were on the radio's LAN, including full VITA-49 UDP relay for waterfall and audio
- **Discovery relay** — broadcasts VITA-49 discovery packets on the LAN so SmartSDR clients find Flexy automatically
- **Web UI** (`:8080`) — live status, configuration, connection tracking, logs

## Quick start

```bash
flexy -radio :discover: -headless -web :8080 -proxy :4992
```

Or with a specific radio IP:

```bash
flexy -radio 10.10.0.10 -headless -web :8080 -proxy :4992
```

## Options

| Flag | Default | Description |
|---|---|---|
| `-radio` | `:discover:` | Radio IP or `:discover:` for auto-discovery |
| `-headless` | false | Create own station (use when no SmartSDR GUI is running) |
| `-station` | `Flex` | Station name to bind to (non-headless mode) |
| `-slice` | `A` | Slice letter to control |
| `-listen` | `:4532` | Hamlib rigctld listen address |
| `-web` | *(disabled)* | Web UI listen address |
| `-proxy` | *(disabled)* | SmartSDR proxy listen address |
| `-proxy-ip` | auto | IP advertised in discovery (what SmartSDR connects to) |
| `-radio-bind-ip` | auto | Local IP to bind when connecting to radio (for VPN setups) |
| `-log-level` | `info` | Log level: trace, debug, info, warn, error |
| `-metering` | true | Subscribe to radio meter UDP stream |

## Install on Linux

```bash
# Build for Linux amd64 and deploy
make install HOST=user@your-linux-box

# Or for ARM64 (Pi 4, etc.)
make install-arm64 HOST=user@your-linux-box
```

This copies the binary to `/usr/local/bin/flexy`, installs `flexy.service`, and enables it via systemd. Edit `/etc/systemd/system/flexy.service` to set your flags, then `systemctl restart flexy`.

## License

MIT — see [LICENSE](LICENSE)

Derived from [nCAT](https://github.com/kc2g-flex-tools/nCAT) by Andrew Rodland — see [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES)
