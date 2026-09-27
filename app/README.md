# Tandem (Go)

One Windows binary, ~8 MB: the window, the tray, the GSX client, the peer link and the
sync engine. Two of them on two PCs make GSX Pro's ground services act as one. No
runtime, no services, nothing to install on the other machine.

    bash build.sh     # tandem.exe, staged into cmd/tandem-setup/payload/, then the
                      # single-file installer and dist/Tandem-<version>.zip

The user-facing manual is **../GUIDE.md**; it is also copied next to the binary and into
the archive, because it is the one file that has to survive a broken install.

## Layout

| Path | What it is |
|---|---|
| `cmd/tandem` | the app. Subcommands: `relay`, `discover`, `fake`, `status`, `stop`, `window` |
| `cmd/tandem-setup` | the installer: one file carrying the program, its own window, `--quiet` / `--dir` / `--uninstall` |
| `internal/syn` | **the sync engine**: phase classifier, intents, digests, reconciliation |
| `internal/gsx` | Couatl client (subscribe / snapshot / patch, serialized commands) |
| `internal/room` | the P2P link: ws host+dial, UDP + fragmentation, STUN, rooms, retransmit |
| `internal/discover` | the code phone book (`tandem discover`) — a code, no addresses |
| `internal/relay` | the optional hop for NATs that cannot be punched |
| `internal/ui` | controller, HTTP/SSE API, embedded dashboard, the app window, the link format |
| `internal/tray` | the tray icon and its menu |
| `internal/fake` | a stand-in Couatl, so the whole stack is testable without the sim |
| `internal/wire` | the types that cross a socket (leaf package, breaks the import cycle) |
| `internal/aircraft` | reads the MSFS community folder, matches what the sim says against it, and keeps a profile per aircraft |
| `internal/conf`, `internal/logx` | `%APPDATA%\Tandem` settings, ring-buffer logger with fan-out |
| `internal/ui/updater.go` | the release fetch: asset choice, length-checked download, checksum, hand-off |
| `test/uiharness.js` | renders the real dashboard against a running app, in Node, no browser |

## Proving it

    go test ./...              # engine, link, room format, invite format, updater
    bash smoke.sh              # two binaries, two fake GSX, one action, one room
    bash smoke-relay.sh        # allowed to meet only through a relay
    bash smoke-code.sh         # joining by 8-character code through a rendezvous
    node test/uiharness.js     # the dashboard's render() against live state

`uiharness.js` exists because every other check misses the one failure mode that
matters: `render()` throwing halfway, leaving a blank page that looks like a dead app.
It also asserts the privacy behaviour — that pressing *Hide* leaves no dialable address
in the DOM, and that an unseen warning cannot be scrolled past.

## Windows, and only Windows

`internal/ui/window_windows.go` renders the dashboard in Edge's app mode
(`--app=…`, its own user-data directory, hideable, own taskbar button) and controls it
with four Win32 calls. Two windows that share a browser profile are *one* window — the
second launch just focuses the first — so anything that shows two of them
(the app and the installer) has to give them separate profiles and separate titles, and
`findWindowBySweep` matches a title exactly rather than by prefix so one cannot steal
the other. If Edge is missing, `OpenBrowser` is the fallback and nothing is lost but the
frame.

## The bugs worth remembering

* One writer per socket. Frames come from the reader (answering an `ask`), the keepalive,
  the engine and the retransmit loop — gorilla panics on a concurrent write. Every
  transport's send is mutex-guarded in `addTransport`.
* The two ports in an invite mean different things. The WebSocket is dialled at our real
  listener port; the UDP punch goes at whatever port our NAT happened to map. STUN only
  ever reports the second one, so swapping them connects to nothing.
* A `REG` sent from a throwaway socket registers the wrong port in the phone book, and an
  introduction that hands a box *its own* address looks identical to success right up
  until nothing connects.
* `net.IP.IsGlobalUnicast` counts `192.168.x.x` as global. Private candidates have to be
  classified by `IsPrivate` first or the LAN sweep is aimed at nothing.
* An installer cannot delete the image it is running from — no handle, no rename, no
  delete-on-close. The sweep is a script handed to the shell, which is the only thing
  that reliably outlives the process.

## Updating over GitHub

`internal/ui/update.go` asks for the newest tag; `updater.go` turns a newer release into a
running installer. The rules it enforces, each covered by a test against a stub server:

* an update is only ever *offered* - the download starts when a person presses it;
* the automatic "Source code" archive of a release is never mistaken for an installer;
* a body that ends short of `Content-Length` is deleted, not executed;
* the SHA-256 GitHub publishes with the asset is verified when present, and its absence is
  logged rather than passed over in silence;
* never downgrade, never install over yourself quietly - the app exits so the installer can
  take the files.

The test that matters most is `TestPickInstallerPrefersTheSingleFile`: it is the one that
found the source-archive bug, because every GitHub release carries a zipball and the first
version of this code took the first `.zip` it saw.
