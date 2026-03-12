# Research: SmartSDR Proxy Protocol — Companion Apps, DAX, Discovery & IP Rewriting
Started: 2026-03-12 | Status: complete

## Sources Consulted

| # | Source | Type | Quality |
|---|--------|------|---------|
| 1 | [flexradio/smartsdr-api-docs wiki](https://github.com/flexradio/smartsdr-api-docs/wiki) | Official API docs | High |
| 2 | TCPIP-client wiki page (BIND, GUI, UDPPORT, STATION, PROGRAM) | Official API | High |
| 3 | TCPIP-stream wiki page (stream create/remove/set) | Official API | High |
| 4 | TCPIP-dax wiki page (dax audio set, dax iq set) | Official API | High |
| 5 | Discovery-protocol wiki page (VITA-49 format, key=value payload) | Official API | High |
| 6 | [K3TZR/xLib6000](https://github.com/K3TZR/xLib6000) — Swift FlexRadio client library | Reference impl | High |
| 7 | [K3TZR/SDRApiFeatures](https://github.com/K3TZR/SDRApiFeatures) — newer Swift library | Reference impl | High |
| 8 | [kc2g-flex-tools/flexclient](https://github.com/kc2g-flex-tools/flexclient) — Go library | Reference impl | High |
| 9 | Flexy proxy.go (existing proxy implementation) | Our code | High |
| 10 | FlexRadio community forums | Attempted | 403 (gated) |

---

## 1. How SmartDAX/SmartCAT Discovers and Binds to a GUI Client Station

### Discovery Packet gui_client_* Fields

The radio's VITA-49 discovery broadcast (UDP port 4992, class code 0xFFFF) includes these fields in the v3+ ("newApi") protocol:

```
gui_client_handles=0x12345678,0x9ABCDEF0
gui_client_hosts=hostname1,hostname2
gui_client_ips=192.168.1.100,192.168.1.101
gui_client_programs=SmartSDR-Win,SmartSDR-Win
gui_client_stations=Station1,Station2
```

These are comma-separated parallel arrays. Each index corresponds to one connected GUI client.

### How xLib6000 Parses These

From `Discovery.swift` (xLib6000), `parseGuiClients()`:
```swift
let programs  = packet.guiClientPrograms.components(separatedBy: ",")
let stations  = packet.guiClientStations.components(separatedBy: ",")
let handles   = packet.guiClientHandles.components(separatedBy: ",")
// hosts and ips are parsed but not used for matching
```

A `GuiClient` struct is created with: `handle`, `station`, `program`, `clientId`, `host`, `ip`.

### Dual Discovery Sources — Discovery vs TCP Subscription

**Discovery broadcasts** (UDP, every ~1s) provide the gui_client_* fields. These are used for the **initial station list** before a TCP connection is established.

**TCP subscription** (`sub client all`) provides real-time `client 0x<handle> connected` status messages with `client_id=<UUID>`, `program=<name>`, `station=<name>`, `ip=<ip>`. After TCP connection, this is the authoritative source for client state.

From xLib6000's `Discovery.swift`, on every discovery packet:
- `processAdditions()` — checks if any new gui_client handles appeared
- `processRemovals()` — checks if any handles disappeared
- Posts `guiClientHasBeenAdded` / `guiClientHasBeenRemoved` notifications

From xLib6000's `Radio.swift`, on TCP `client` status messages:
- `parseV3Connection()` — updates GuiClient with clientId, program, station, isLocalPtt
- Posts `guiClientHasBeenUpdated` notification

### Station Binding Flow (Non-GUI Client like SmartDAX)

From xLib6000 `Api.swift` documentation comment:
```
Scenario 3: Client connects as non-Gui, binding is desired, ClientId is known
  - Client passes clientId=<ClientId>, isGui=false to the Api
  - Api sends "client bind client_id=<ClientId>" command to the Radio

Scenario 4: Client connects as non-Gui, binding is desired, ClientId is NOT known
  - Client passes clientId=nil, isGui=false
  - Client receives GuiClientHasBeenAdded notifications
  - Client finds the desired ClientId
  - Client sets boundClientId property
  - Api sends "client bind client_id=<ClientId>" to the Radio
```

From TCPIP-client wiki on BIND:
> "From a radio perspective as of v3.0, this command is simply for debugging and troubleshooting... but in and of itself performs no function in the radio."

**Key finding**: `client bind` is **informational only** in the radio. The companion app uses it to declare which GUI client it follows, but the radio doesn't enforce anything based on it.

### What Happens When gui_client_* Fields Are Stripped from Discovery

**Stripping gui_client_* fields from relayed discovery prevents companion apps from populating their station list if they depend solely on discovery data for initial population.**

However, from the xLib6000 code, the companion app has TWO paths to learn about stations:

1. **Discovery packets** (gui_client_* fields) — initial station list
2. **TCP subscription** (after connecting) — `sub client all` delivers client connect/disconnect status

If gui_client_* fields are stripped, the companion app must:
- Connect to the radio (via the proxy) first
- Subscribe to client status to discover stations

**The problem with a proxy**: When gui_client_* fields in discovery contain radio-LAN IPs (e.g., `gui_client_ips=192.168.1.100`) but the companion sees the proxy on a different IP (e.g., `100.64.1.5`), there's a **mismatch**. xLib6000 re-evaluates its station list every discovery tick, comparing discovery gui_client_ips against known clients. This mismatch causes the station list to be **repeatedly cleared**.

**Stripping is the correct approach** for a proxy — it forces companion apps to rely solely on TCP subscription data, which the proxy can rewrite consistently.

This is confirmed by the comment in the existing Flexy proxy code:
```go
// Companion apps (Smart CAT, Smart DAX) re-evaluate their station
// list against these fields on every discovery tick. When the IPs
// in discovery (radio-side LAN) don't match what the companion sees
// via the TCP subscription (also radio-side LAN, un-rewritten), the
// station list gets cleared. Omitting these fields forces companion
// apps to rely solely on TCP subscription data, which works correctly.
// This mirrors the approach used by SmartUnlink.
```

---

## 2. The `stream create type=dax_rx` Command Flow and VITA-49 Audio Routing

### Stream Creation Sequence

From the official API wiki (TCPIP-stream):
```
C<seq>|stream create type=dax_rx dax_channel=<num>
```

Response:
```
R<seq>|0|<stream_id>
```

The radio then starts sending VITA-49 UDP packets with that stream_id as the VITA-49 Stream ID field.

### Available Stream Types (from K3TZR SDRApiFeatures StreamModel.swift):
```
stream create type=dax_rx dax_channel=<N>           // DAX RX audio
stream create type=dax_tx                            // DAX TX audio
stream create type=dax_mic                           // DAX microphone audio
stream create type=dax_iq daxiq_channel=<N>          // DAX IQ data
stream create type=remote_audio_rx compression=none|opus  // Remote RX (WAN)
stream create type=remote_audio_tx                   // Remote TX (WAN)
```

### How the Radio Determines Where to Send VITA-49 Audio

**The radio sends VITA-49 UDP packets to: TCP socket remote IP + registered udpport.**

Evidence:

1. From `flexclient/client.go` `InitUDP()`:
```go
func (f *FlexClient) InitUDP() error {
    udpConn, err := net.ListenUDP("udp", &net.UDPAddr{Port: f.udpListenPort})
    // ...
    res := f.SendAndWait(fmt.Sprintf("client udpport %d", f.udpPort()))
}
```

2. From K3TZR SDRApiFeatures `ObjectModel.swift`:
```swift
if packet.source == .local {
    sendTcp("client udpport " + "\(_udp.sendPort)")
}
```

3. From the official API wiki (TCPIP-client, UDPPORT):
> "Provides the radio with the UDP port that should be used on the client to receive streaming VITA-49 UDP data."

4. The `stream create` error code `50000064` = "No IP or PORT" — confirms both are needed.

**The radio uses the TCP connection's remote IP (source IP of the TCP socket) combined with the port from `client udpport <N>` as the UDP destination for all VITA-49 streams belonging to that client.**

This is why the existing Flexy proxy rewrites `client udpport`:
```go
if m := udpPortRe.FindStringSubmatch(line); m != nil {
    clientPort, _ := strconv.Atoi(m[2])
    localPort, counter, err := startUDPRelay(bindIP, clientAddr.IP.String(), clientPort, done)
    // Replace client's port with the relay's local port
    line = m[1] + strconv.Itoa(localPort)
}
```

The proxy sends the relay port to the radio instead. The radio then sends UDP to `proxyIP:relayPort`, and the relay forwards to `clientIP:clientPort`.

### VITA-49 Class Codes for Audio (from Vita.swift):
```
daxAudio          = 0x03E3   // Full bandwidth
daxAudioReducedBw = 0x0123   // Reduced bandwidth
daxIq24           = 0x02E3
daxIq48           = 0x02E4
daxIq96           = 0x02E5
daxIq192          = 0x02E6
meter             = 0x8002
opus              = 0x8005   // Remote audio (compressed)
panadapter        = 0x8003
waterfall         = 0x8004
```

### DAX Audio Frame Format (DaxRxAudioFrame from StreamFrames.swift):
- Normal bandwidth: Interleaved big-endian Float32 stereo (L, R, L, R...)
- Sample rate: 24,000 Hz
- 2 channels
- Reduced bandwidth: Int16 mono, 24,000 Hz, 1 channel

---

## 3. Port 4992 Unicast Discovery — Does It Work?

### The Port Conflict Problem

- **Discovery broadcasts**: Radio sends VITA-49 discovery to UDP port **4992** (broadcast)
- **TCP API**: Radio listens for TCP connections on port **4992**
- **SmartSDR client**: Listens for discovery broadcasts on UDP **4992**

When SmartSDR is running on a machine, it binds to UDP 4992 for discovery listening. If the proxy also tries to send unicast discovery to port 4992 on that machine, it will be received by whichever socket has `SO_REUSEPORT` set.

### Evidence from Flexy's Design

The existing code uses two ports:
```go
discoveryPort      = 4992  // LAN broadcast
discoveryRelayPort = 4993  // unicast to flexy-discovery peers
```

The comment in the code:
```go
discoveryRelayPort = 4993 // unicast to flexy-discovery peers (avoids 4992 conflict with SmartSDR)
```

This exists because:
1. On LAN, broadcast to port 4992 works because all listeners on the broadcast address receive it
2. For Tailscale/VPN unicast, sending to port 4992 could conflict with SmartSDR's own port 4992 UDP listener
3. The `flexy-discovery` companion binary listens on 4993 and re-broadcasts locally

### From flexclient Discovery

`discoveryListen()` in flexclient uses `SO_REUSEPORT` on Unix:
```go
func discoveryListen() (*net.UDPConn, error) {
    lc := net.ListenConfig{
        Control: func(_, _ string, c syscall.RawConn) error {
            opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
        },
    }
    conn, err := lc.ListenPacket(context.Background(), "udp", ":4992")
}
```

### Verdict

**Port 4992 unicast CAN work** if:
- The recipient uses `SO_REUSEPORT` (Unix) or `SO_REUSEADDR` (Windows)
- There's no conflicting app that binds 4992 exclusively

**But it's unreliable** because:
- SmartSDR for Windows binds UDP 4992 and may not use `SO_REUSEPORT`
- On Windows, `SO_REUSEADDR` behaves differently (last bind wins)
- Using a separate port (4993) with a local re-broadcaster is more robust

---

## 4. IP Rewrites a Proxy Must Do

### Rewrites Currently Implemented in Flexy

1. **Discovery `ip=` field**: `ip=<radioIP>` → `ip=<proxyIP>` — so clients connect to the proxy, not the radio directly

2. **Discovery nickname**: Appends `[Flexy]` for UI distinction

3. **Discovery gui_client_* stripping**: Removes `gui_client_handles`, `gui_client_hosts`, `gui_client_ips`, `gui_client_programs`, `gui_client_stations` — prevents IP mismatch

4. **`client udpport` interception**: Proxy captures the client's UDP port, starts a relay on a local port, sends the relay port to the radio instead

5. **`client ip` response rewrite**: The radio's response to `client ip` returns the TCP socket's remote IP (which is the proxy's LAN IP). Flexy rewrites this to the real client IP.

6. **Status `ip=<radioIP>` rewrite**: In radio→client direction, all occurrences of `ip=<radioIP>` are replaced with `ip=<proxyIP>`

### Additional Rewrites That MAY Be Needed

**Client IP addresses in `sub client all` status messages:**

When a GUI client connects via the proxy, the radio sees the proxy's IP as the client's IP. The `client 0x<handle> connected` status message will contain:
```
|client 0x<handle> connected client_id=<uuid> program=<name> station=<name> ip=<proxyLanIP>
```

If companion apps cross-reference the `ip` in client status with their own view of the world, this could be an issue. However:
- From the API docs, the `ip` field in client status is for **debugging** information
- SmartDAX binds by `client_id`, not by `client ip`
- The `client bind` command uses `client_id=<UUID>`, not an IP address

**Stream status `ip=` field:**

When a stream is created, the radio broadcasts a status like:
```
|dax_rx 0x<streamId> type=dax_rx dax_channel=2 slice=A client_handle=0x<handle> ip=<radioSeenIP>
```

The `ip` field here is what the radio sees as the client's IP. K3TZR's code (`DaxRxAudio.swift`) parses this but uses `client_handle` for ownership matching, not `ip`.

**Conclusion**: Beyond what Flexy already does, the most critical additional rewrite would be:
- **Client status `ip=` rewriting** in `sub client all` responses, if companion apps use these IPs for display or matching
- This is likely low priority since binding is by client_id, not IP

---

## 5. Existing SmartSDR Proxy Implementations

### SmartUnlink

- **Not found on GitHub** (proprietary/closed-source Windows application)
- Referenced in Flexy's code comments as using the gui_client_* stripping approach
- Commercial product for FlexRadio remote operation
- Known to handle: TCP proxy, UDP relay, discovery relay, IP rewriting

### xDAX Remote

- **Not found on GitHub** (likely proprietary)
- Commercial product from Master Communicator Technologies
- Provides DAX audio over network (WAN)

### K3TZR SDRApi / xLib6000

- **Open-source Swift implementation** (macOS)
- Not a proxy, but a full client library
- Key insights extracted:
  - Discovery parsing of gui_client_* fields
  - GuiClient struct with handle/station/program/clientId/ip
  - `client bind client_id=<UUID>` for non-GUI companion binding
  - `client ip` response handling for WAN connections
  - Stream creation commands and VITA-49 class code handling
  - WAN connection uses `wan validate handle=<wanHandle>` + `client udp_register`

### FlexLib .NET (Official)

- Referenced in the API wiki but not separately on GitHub
- The official .NET library for SmartSDR
- WAN server functionality in `Wan.swift` / `WanServer.swift` (in xLib6000):
  - `wan set public_tls_port=<port> public_udp_port=<port>`
  - SmartLink uses TLS for TCP and a public UDP port for VITA-49

### kc2g-flex-tools/flexclient (Go)

- Open-source Go library
- Key implementation details:
  - `InitUDP()`: binds local UDP port, sends `client udpport <N>`
  - `InitWANUDP()`: dials a pre-connected UDP socket for WAN, sends `client udp_register handle=0x<handle>`
  - Discovery uses `SO_REUSEPORT` on Unix
  - UDP default destination: radio IP port 4991
  - Class code routing: meter (0x8002), FFT (0x8003), PCM/daxAudio (0x03E3), opus (0x8005)

---

## 6. Complete TCP Command Sequence for a SmartDAX-like Companion

Based on K3TZR's xLib6000 `Api.swift` and the API documentation:

### GUI Client (SmartSDR) Initial Sequence:
```
← V3.x.x.x                           # radio sends version
← H<handle>                           # radio assigns handle
→ client gui <clientId>               # register as GUI with persistent UUID
→ client program SmartSDR-Win
→ client station MyStation
→ info
→ version
→ antenna-list
→ mic list
→ profile global info
→ profile tx info
→ profile mic info
→ profile display info
→ sub slice all
→ sub client all                      # subscribe to client status updates
→ sub tx all
→ sub meter all
→ sub atu all
→ sub gps all
→ sub amplifier all
→ sub memory all
→ sub usbcable all
→ sub xvtr all
→ sub spot all
→ client udpport <N>                  # register UDP port for VITA-49
→ radio set mtu_limit 1500
→ client set low_bw_dax=0
```

### Non-GUI Companion (SmartDAX) Initial Sequence:
```
← V3.x.x.x                           # radio sends version
← H<handle>                           # radio assigns handle
→ client program SmartDAX
→ client bind client_id=<UUID>        # bind to a GUI client's UUID (informational)
→ sub client all                      # subscribe to learn about GUI clients
→ client udpport <N>                  # register UDP port
→ stream create type=dax_rx dax_channel=1
← R<seq>|0|<streamId>                # radio assigns stream ID
→ dax audio set 1 slice=0            # assign DAX channel 1 to slice 0
```

### Key Points:
- SmartDAX does NOT send `client gui` — it's a non-GUI client
- It uses `client bind client_id=<UUID>` to associate with a GUI station
- It creates its own stream with `stream create type=dax_rx`
- The radio sends VITA-49 audio to the companion's `TCP_remote_IP:udpport`

---

## 7. Known Issues with DAX Through a Proxy/Relay

### Latency
- DAX audio at 24kHz, Float32 stereo = ~192 KB/s per channel per direction
- Each relay hop adds buffer + copy latency
- For digital modes (WSJT-X): latency is acceptable (decode windows are 15s+)
- For voice monitoring: may be perceptible with high-latency links (>50ms RTT)

### NAT Traversal
- The radio sends UDP to `TCP_remote_IP:udpport`
- If there's NAT between the proxy and radio, the proxy's source IP in the TCP connection determines where UDP goes
- The existing proxy sends NAT keepalives to radio:4991 to maintain hole-punching
- Tailscale CGNAT (100.64.0.0/10) works because Tailscale handles the NAT traversal at the WireGuard level

### Stream Ownership and Cleanup
- When a proxy client disconnects, orphaned streams/pans/slices on the radio need cleanup
- Flexy handles this in `cleanupProxyClient()` by removing owned pans and slices
- Streams are automatically cleaned up by the radio when the TCP connection drops

### Companion App IP Confusion
- If a companion connects through the proxy, the radio sees the proxy's IP
- Status messages about that companion's streams will show the proxy's IP, not the companion's
- This is mostly cosmetic but could confuse debugging

### Multiple Companion Apps
- Multiple companions (SmartDAX + SmartCAT) can connect through the proxy simultaneously
- Each gets its own TCP connection, handle, and UDP relay
- The proxy must track and relay UDP for each independently

### Reduced Bandwidth Mode
- SmartSDR supports `low_bw_dax` mode for WAN connections
- Sends Int16 mono instead of Float32 stereo (1/4 bandwidth)
- The proxy passes this through transparently

---

## 8. Summary of Required Proxy Operations

### TCP Proxy (Port 4992)
| Operation | Direction | What | Why |
|-----------|-----------|------|-----|
| Pass through | Both | All commands/responses | Core proxy function |
| Intercept | Client→Radio | `client udpport <N>` | Replace with relay port |
| Intercept | Radio→Client | `client ip` response | Replace proxy LAN IP with real client IP |
| Rewrite | Radio→Client | `ip=<radioIP>` → `ip=<proxyIP>` | Consistent IP view |
| Track | Both | Handle (H-line) | Know client's handle for cleanup |
| Track | Both | Program, station names | Logging/debugging |
| Track | Radio→Client | Pan/slice ownership | Cleanup on disconnect |

### UDP Relay (Per-client, any port)
| Operation | Direction | What |
|-----------|-----------|------|
| Relay | Radio→Client | All VITA-49 packets to client |
| Relay | Client→Radio | All VITA-49 packets to radio:4991 |
| NAT keepalive | Proxy→Radio | Periodic UDP to radio:4991 |

### Discovery Relay (UDP 4992 → broadcast + unicast)
| Operation | What |
|-----------|------|
| Receive | Radio's broadcast discovery on UDP 4992 |
| Rewrite | `ip=<radioIP>` → `ip=<proxyIP>` |
| Strip | All `gui_client_*` fields |
| Annotate | Append `[Flexy]` to nickname |
| Broadcast | Send to all LAN broadcast addresses on port 4992 |
| Unicast | Send to Tailscale peers on port 4993 |

---

METRICS: searches=8 fetches=10 high_quality=8 ratio=0.8
CHECKS: [x] freshness [x] went_deep [x] found_outlier [x] checked_awesome
