# Tandem

Shared ground-equipment sync for **FSDT GSX Pro** on **MSFS 2024** — two cockpits, one
ramp. When either side orders catering, a GPU or a pushback, the other cockpit's GSX
does it too, with its own trucks and its own crew, because GSX servicers are local AI
objects and never replicate over multiplayer. Tandem mirrors the *intent* and replays it
locally, so both ramps look identical.

Nothing about the simulator itself is touched: no simmods, no aircraft or FMC state, no
multiplayer traffic. It speaks to GSX's own Remote control server on each box.

**Using it** (no build, no code): one file, `tandem-setup.exe`, which carries the program
inside it. Read [GUIDE.md](GUIDE.md) — that file is the whole manual and is installed next
to the program too.

## What is in here

| Path | What it is |
|---|---|
| `app/` | the product: one Go binary, ~8 MB, embedded dashboard, no runtime, no services |
| `app/cmd/tandem` | the app: window, tray, GSX client, peer link, sync engine |
| `app/cmd/tandem-setup` | the installer: one file, a window of its own, `--quiet` for scripts |
| `tandem relay`, `tandem discover` | the two optional servers, both subcommands of the same binary |
| `app/internal/` | the parts, each testable without a simulator |
| `GUIDE.md` | the manual that ships inside the archive |
| `TEXTLOGO.svg`, `ICON.ico` | the wordmark and its ICO flatten |

## Build

Go 1.22+, Windows. From `app/`:

    bash build.sh            # tandem.exe, then tandem-setup.exe carrying it, then the zip

Artifacts land in `app/dist/`, which is not tracked. The `.exe`s are unsigned; SmartScreen
will complain once and `More info → Run anyway` is the correct answer for a build from
this repo.

## Prove it

    cd app && go test ./...          # the engine, the link, the room format, the wire
    bash app/smoke.sh                # two real binaries, two fake GSX, one action, one room
    bash app/smoke-relay.sh          # the same, allowed to meet only through a relay
    bash app/smoke-code.sh           # joining by 8-character code through a rendezvous
    node app/test/uiharness.js 18795 # renders the real dashboard against a running app

The dashboard is HTML+JS embedded in the binary, and the harness exists because every
other check misses the one failure that matters: a `render()` that throws halfway and
leaves a blank page that looks like a dead app.

## The two rules worth knowing

* A progress-bar tick is never read as an action.
* A service the other cockpit started *for you* is never broadcast back at them.

Everything else in `internal/syn` exists to keep those two true while GSX changes its
mind mid-service.

## History

The protocol was first written in JavaScript against a live GSX (`src/`, in the git
history) to find out what Couatl actually sends. The semantics were then ported to Go
and that is what ships. The JS tree is not built or shipped any more.
