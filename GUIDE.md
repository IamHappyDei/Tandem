# Tandem — the guide

Two cockpits, one ramp. A service ordered on either side is done on both. No account,
no subscription, nothing running in between: the two machines talk to each other and to
your own copy of GSX.

---

## 1. Install (once per PC)

There is one file: **`tandem-setup.exe`** (~16 MB; the archive it travels in is ~7 MB). It
carries the program, the icon, this guide and the firewall helper inside itself — nothing
to unzip beside it, nothing to download at install time.

Double-click it. Windows asks for administrator rights once, up front, so the window can
install to either place without interrupting you later. If you answer *No*, the window
still opens and *Only for me* works without any rights.

- **For every account on this PC** — into `C:\Program Files\Tandem`.
- **Only for me** — into your own profile.
- **Let Tandem through Windows Firewall (needed in order to let others connect)** — leave
  it on unless you know why not to.
  Without an inbound rule Windows quietly drops the other cockpit's packets and both
  screens sit there saying "alone" with no error anywhere. This is the single most
  common reason a session never forms.
- **Start Tandem when I sign in**.

It is unsigned, so SmartScreen will warn: **More info → Run anyway**. Afterwards find
Tandem in the Start menu, or run `tandem-setup.exe` again to update or remove it. An
installer that was run from inside the folder it installs to clears that folder properly,
including the file it was running from.

Uninstall from **Settings → Apps**, or run `tandem-setup.exe --uninstall`.

## 2. Both cockpits, every time

1. In MSFS: **GSX Settings → Remote control server → Enabled**. It then listens on
   `ws://127.0.0.1:8744`. No simulator restart needed.
2. Start Tandem. A window opens — that *is* the app, not a browser tab. Closing or
   minimising it does not stop the sync; the tray icon has Show / Hide / Quit.
3. Read the first warning once. Then it stops asking.

## 3. Connect

Tandem has a room of its own from the moment it starts; there is no "open a room"
button, and nothing to keep running.

**The string.** One of you copies the line in the big card and sends it — over Discord,
over anything. It looks like

```
tdm1/203.0.113.44:8790+8791/KQ7TB
```

— reach me here, dial this port, punch that UDP port, join this room. The other person
pastes it into the box, presses **Connect**, and about ten seconds later both windows
say *You and … share one ramp*. Order a Boarding anywhere and both ramps move.

If a passphrase is set in Settings it rides along at the end as `#phrase`, and a wrong
one is refused.

**The code.** Your 8 characters are shown under the join box, with a badge that says
whether they are any use yet. A code contains no address, so it only works when both
boxes already know the same small phone-book server: put `host:port` in
Settings → *rendezvous*, or run `tandem discover --port 8788` on any machine you can
both reach — a VPS, one of the two PCs, a box at the club. Once the badge reads
**LIVE**, giving someone your code is enough: no IP changes hands, and it survives
you both reconnecting on different addresses. Until then, use the string.

## 4. How much the link gives away

The string contains your public address, in clear text. That is exactly what the other
seat needs and exactly what a stranger should not keep. Settings → *the link* has two
switches and the card tells you which shape you are holding:

| | the string | what it gives a bystander |
|---|---|---|
| **plain** (default) | `tdm1/203.0.113.44:8790+8791/KQ7TB` | your address, readable |
| **scrambled** | `tdm1~V7kq2…` (about 200 characters) | nothing — no address, no room, no port |
| **long form** (only when STUN heard nothing) | `tdm1:eyJ…` | every address this PC has, in base64 |

Scrambling is not a wall, it is a curtain, and the app says so rather than pretending
otherwise: the person you send it to opens it as normal and reads the address inside —
that is what an invite is for. What it stops is a link pasted in a group chat, a forum,
or a screenshot that outlives the flight, handing out your address to everyone else who
sees it. If the room has a passphrase, that passphrase is mixed into the key, so a
scrambled string found in the wild opens for nobody.

**Keep it masked here** blurs your own address in the window — for someone standing
behind you. Copy still hands over the real string.

The honest limit: none of this hides your address from the machine you connect to. That
is what a direct connection *is*. If you need that hidden too, run through a relay —
then the only address in your link is the relay's.

## 5. When punching is hopeless

Some routers (symmetric NAT, carrier-grade NAT) cannot be punched at all, and the app
will tell you instead of pretending: **"This box has no public address"**, or a
"Looking for them…" that never ends.

```
tandem relay --port 8791
```

on any machine both of you can reach, then `ws://that-address:8791` in
Settings → *relay* on both cockpits. Frames then hop instead of punching. Slower by a
few milliseconds, and it works everywhere.

## 6. Getting an update

If update checks are on (Settings → the switch, off-change-free), Tandem asks GitHub every
six hours whether a newer release exists and, if one does, says so in the footer. **Nothing
is downloaded and nothing is replaced until you press it.**

Pressing **Download & install** does exactly this, in order:

1. reads the newest release of the GitHub project and picks the installer from it —
   `tandem-setup.exe`, the same single file you would have downloaded by hand;
2. writes it to a temp folder, watching the length so a download that stops half way is
   deleted instead of run;
3. checks its SHA-256 against the checksum GitHub records for the asset. If the release has
   no checksum, it says so in the log and carries on — that is the honest weakness of the
   path, not a silent pass;
4. closes this copy of Tandem (so the files on disk are free — your co-pilot sees the
   connection drop), and starts the new installer. The installer then elevates, writes the
   files and starts Tandem again.

From a terminal, the same thing against the copy that is running:

    tandem update              what did it find
    tandem update --get        fetch it and run the installer

Or skip all of it: download `tandem-setup.exe` from the release page and run it. Version
numbers are `major.minor.patch`, and an update never downgrades.

### Publishing one, if you are the one building it

`bash build.sh` leaves `app/dist/tandem-setup.exe` and `dist/Tandem-<version>.zip`. Tag it,
push the tag, then:

    cd app && bash release.sh v1.1.0 notes.md   # needs the tag pushed

That creates the Release, **attaches `tandem-setup.exe`** — GitHub records its SHA-256 and
the updater verifies against it — and takes the zip along with it. Run it twice and it
replaces the files rather than failing: a corrected build can be republished without a new
tag. A zip whose name contains `Tandem` works as the attached file too, but the automatic
"Source code" archive is deliberately ignored: every release has one, and it is not an
installer.

## 7. Switches: what is shared, and with which aircraft

Two switches, both saved, both applied the moment you move them — no restart.

**Share GSX ground services** (Settings, and a button on the *ground services* card).
Off means: the room stays connected, your co-pilot still sees you there, notes still
cross, and **nothing is ordered in either cockpit's GSX** — no trigger, no menu pick, no
drift correction. Drift is still reported in the log, because refusing to act on a
difference is not the same as not seeing it.

**Pause Tandem entirely** (Settings). The hard stop. Nothing is sent, nothing is applied,
the phone book and the relay keep the connection warm so resuming costs nothing. The footer
says *paused* and the card says so in words — a paused window never claims to be sharing.

Both are honest in `/api/status`: `sync.mode` is `gsx + room`, `room only` or `paused`, and
`sync.counters.muted` counts the orders that were noted and refused.

### Aircraft profiles

Tandem can read your MSFS community folder and keep a profile per aircraft: whether that
type shares ground services, and which services you care about. The folder is found by
looking in the usual places and keeping whichever one actually holds aircraft — the first
directory called `Community` is not it, on a box with three. Say so in Settings if your
folder lives somewhere else, and press **Rescan folder** after you install something.

Under the picker, the services GSX has told us about appear as chips you can tick. Ticked
means *mirror this one for this aircraft*; unticked, an order for it is noted and refused —
counted in `sync.counters.filtered`, never dropped in silence. Leave them all ticked and
every service is mirrored, including ones the sim has not mentioned yet.

**Detect** matches the aircraft name GSX reports against the folder and says where the name
came from. When nothing matches it says so and you pick from the list; a profile you never
picked is not applied to a type Tandem only guessed at. Profiles are per aircraft, so a
profile that turns GSX sharing off for the A321 leaves every other aircraft as it was.

This is also the way back: if something in a new version behaves wrong, pause, put the old
`tandem-setup.exe` back, resume. Your config and profiles are kept in `%APPDATA%\Tandem`
and nothing in an installer touches them.

## 8. Command line

```
tandem                       window + tray — this is the app
tandem --nogui               no window, no tray (a box that only syncs)
tandem --browser             the dashboard in your normal browser
tandem --console             keep the console open, log to it too
tandem status                what the running copy sees, as JSON
tandem version               the version this file is, and nothing else
tandem update                ask GitHub for the newest release and say what it found
tandem update --get          download its installer and run it (this copy closes)
tandem stop                  ask it to quit
tandem window hide|show      the window, from a terminal
tandem relay --port 8791     the optional relay
tandem discover --port 8788  the code phone book
tandem fake --port 8744      a stand-in GSX, for testing without the sim

tandem-setup.exe                        the graphical installer, elevated on start
tandem-setup.exe --no-elevate           the same window, without asking again
tandem-setup.exe --dir "D:\Tandem"      install somewhere else
tandem-setup.exe --uninstall --quiet    for scripts and GPO
```

## 9. Reading the window

| It says | Meaning |
|---|---|
| **Send this string to them** | ready and alone — copy it, send it |
| **Looking for them…** | dialling and punching; normal for about ten seconds |
| *(that, for over 25 s)* | a firewall rule is missing on one side, or a router no punch can pass |
| **You and … share one ramp** | connected; anything either side does appears below |
| **GSX is not answering** | the Remote control server is off. Nothing else matters until it is on |
| **This box has no public address** | your router drops the STUN probes. Same-network links still work; another city needs a relay |

Below the card: the services GSX knows about, who is in the room, and a running list of
what just happened. **Settings** and **What the app is doing** are the two drawers at
the bottom — the second shows the addresses, ports, traffic counters and the live log.
Everything refreshes by itself.

## 10. When something looks wrong

| Symptom | Cause | Fix |
|---|---|---|
| both say "alone", nothing in the log | Windows Firewall on one side | re-run the setup with the firewall box ticked, restart Tandem |
| it worked yesterday, not today | your public address changed | make a fresh string, or use a code with a rendezvous |
| "GSX is not answering" | Remote control server off, or MSFS not running | GSX Settings → enable it |
| the log says "a local address" | you are on the same network | fine there; from another city it cannot work |
| two copies on one PC never meet | home routers will not loop back to their own address | expected — use the relay, or the real second PC |
| ports need moving | something owns 8790 | Settings → room/udp port, restart the app |
| no window, a browser tab instead | no Edge on that box | install Edge, or run `tandem --browser` deliberately |
| a scrambled string refuses to open | the room has a passphrase it was made with | type that passphrase in Settings first |
| the update says "no installer is attached" | the release was made from a tag only | attach `tandem-setup.exe` to the release, or download it from the tag page |

The log is `%APPDATA%\Tandem\tandem.log`.

## 11. What it does to your sim

It sends GSX actions — the same ones the GSX menu sends, through GSX's own Remote
control server. It reads no memory, installs no add-on, touches no aircraft, FMC or
multiplayer state, and modifies nothing about the simulator. Unsynced services are
reconciled every five seconds, and an action the two of you caused together is
recognised as an echo rather than replayed back, so nothing loops. Removing Tandem
leaves nothing behind except your settings folder, and `--uninstall --purge` takes even
that.
