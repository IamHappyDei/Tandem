Tandem 1.2.0 — a switch for sharing, and a way for Tandem to hear the aircraft itself

**Shared cockpit is a switch now, and it is off on a fresh install.** Settings has
*Shared cockpit*: off, Tandem joins no room, opens no port, hands out no invite, and says so in
the log and in the API instead of quietly doing it anyway; GSX syncing works exactly as before.
On, the room, the invite string, the relays and LAN discovery all come back without restarting
the app. If you were already using it, your setting is kept — the switch is only off for a
config that never had one.

**The aircraft's own switches (new, off by default).** Tandem can listen on
`ws://127.0.0.1:8796/` and the installer puts a small page into your Community folder for the
sim to talk back through. `tandem-sim` asks SimConnect directly — it finds the DLL next to
itself, in the registry's install path, or where you point it, and ships nothing of Microsoft's
inside the installer. An aircraft profile lists which simvars may be read or written; anything
not listed is not read, not written, and not sent to a coworker. This is new and it has not been
run against a live sim by the author, which is why it is off until you choose it.

**Detection tells the truth about what it found.** It asks the sim, then GSX, then the flight
files, and says which answered. Livery, paint and texture folders can no longer win an aircraft
match — an A321 was being read as "A320 EasyJet G-UZHN", a paint folder that had no business in
the list. When nothing in the folder matches what the sim reported, it says so instead of naming
a near miss.

**The installer asks where your aircraft live.** Prefilled with what it finds, with a *Find it*
button, and a tick box that puts the bridge page in that folder as its own marked directory.
Uninstall removes that folder and nothing else. `--community <folder>` and `--no-bridge` for an
unattended run.

**The icon was invisible.** It was a white glyph on a transparent canvas, so on a light
Explorer background there was nothing to see. It is a dark rounded tile with the same mark now,
with proper frames from 16 to 256, and the tray icon matches. If your desktop still shows the
old blank one, that is the Windows icon cache.

Also carried from 1.1.0: the GSX sync switch and the hard Pause, per-aircraft profiles, and an
updater that stops pretending it has nothing to give you when a release clearly does.

Rolling back: turn both switches off and this is 1.1.0's behaviour — nothing leaves the machine
and nothing is read from the sim. Your config, profiles and room code are untouched.

Assets: `tandem-setup.exe` (the installer; a `.zip` with the same files is attached for people
who'd rather unpack it themselves). SHA-256 is published with the release and checked before the
installer is run by `tandem update`.
