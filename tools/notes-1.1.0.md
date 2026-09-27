Tandem 1.1.0 — two switches, and the aircraft you are actually flying.

**What changed since 1.0.0**

* **GSX sync on/off** — a button on the ground-services card and a switch in Settings. Off
  means the room stays connected, your co-pilot still sees you, notes still cross, and
  nothing is ordered in either cockpit's GSX. Drift is still reported: not acting on a
  difference is not the same as not seeing one.
* **Pause Tandem entirely** — the hard stop. Nothing sent, nothing applied, connection kept
  warm so resuming costs nothing. Both switches persist, apply live, and get a line in the
  log the moment you move them.
* **Aircraft profiles** — Tandem reads your MSFS community folder (it picks the one that
  actually holds aircraft, not the first directory called `Community`), lists what is in it,
  and keeps a profile per aircraft: whether ground services are shared for that type, and
  which services. The services GSX has mentioned appear as chips you can tick. An empty
  profile means everything, so a machine with no profiles behaves exactly as 1.0.0 did.
* **The update offer tells the truth** — it used to print "no installer is attached" for a
  release that had one, because it threw away both errors it got back. Now it says what
  stopped it. The download itself was on a twenty second leash (the HTTP client timeout
  covered the whole body, not just the handshake): a slow line lost the 16 MB installer at
  twenty seconds every time. Downloads now have five minutes and a bounded handshake.
* **Fixed** — a race on the pending-menu queue (read unlocked from three goroutines while
  another wrote it), `saveConfig` claiming a restart was needed for switches that apply
  live, and a status field that reported the two switches in the wrong order.

**Install** — run `tandem-setup.exe` below. It is one file: the program, the icon, the
manual and the firewall helper. Windows may warn about an unsigned publisher; that is about
the certificate, not the contents, and the source is here.

**From a terminal** — `tandem version`, `tandem update`, `tandem update --get`,
`tandem status`, `tandem stop`. `GUIDE.md` is the whole manual and ships inside the installer.
