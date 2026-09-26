# Tandem — the guide

Two cockpits, one ramp. A service ordered on either side is done on both. No account,
no subscription, nothing running in between: the two machines talk to each other and to
your own copy of GSX.

---

## 1. Install (once per PC)

Unzip the archive and double-click **`tandem-setup.exe`**. It is a window, not a
command prompt:

- **For every account on this PC** — into `C:\Program Files\Tandem`. Windows asks for
  administrator rights once, and only when you pick this.
- **Only for me** — into your own profile, no administrator rights at all.
- **Let Tandem through Windows Firewall** — leave it on unless you know why not to.
  Without an inbound rule Windows quietly drops the other cockpit's packets and both
  screens sit there saying "alone" with no error anywhere. This is the single most
  common reason a session never forms.
- **Start Tandem when I sign in**.

It is unsigned, so SmartScreen will warn: **More info → Run anyway**. After installing,
find Tandem in the Start menu; the unzipped folder is no longer needed and the setup
will clear it if you run the setup from inside it.

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

## 6. Command line

```
tandem                       window + tray — this is the app
tandem --nogui               no window, no tray (a box that only syncs)
tandem --browser             the dashboard in your normal browser
tandem --console             keep the console open, log to it too
tandem status                what the running copy sees, as JSON
tandem update                ask GitHub for the newest release and say what it found
tandem stop                  ask it to quit
tandem window hide|show      the window, from a terminal
tandem relay --port 8791     the optional relay
tandem discover --port 8788  the code phone book
tandem fake --port 8744      a stand-in GSX, for testing without the sim

tandem-setup.exe                        the graphical installer
tandem-setup.exe --dir "D:\Tandem"      install somewhere else
tandem-setup.exe --uninstall --quiet    for scripts and GPO
```

## 7. Reading the window

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

## 8. When something looks wrong

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

The log is `%APPDATA%\Tandem\tandem.log`.

## 9. What it does to your sim

It sends GSX actions — the same ones the GSX menu sends, through GSX's own Remote
control server. It reads no memory, installs no add-on, touches no aircraft, FMC or
multiplayer state, and modifies nothing about the simulator. Unsynced services are
reconciled every five seconds, and an action the two of you caused together is
recognised as an echo rather than replayed back, so nothing loops. Removing Tandem
leaves nothing behind except your settings folder, and `--uninstall --purge` takes even
that.
