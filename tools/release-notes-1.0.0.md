Two cockpits, one ramp. Tandem mirrors GSX intent between them and replays it through each
sim's own Couatl, so a service ordered in one cockpit happens in the other.

This release is the one you install and then forget about.

**What is new in 1.0.0**

* A single file to run: `tandem-setup.exe` carries the program, the icon, the manual and the
  firewall helper inside itself. It asks for administrator rights once, up front.
* An update you press, not one that happens to you. Tandem asks GitHub every six hours
  whether a newer version exists and says so in the footer. Nothing is downloaded until you
  click **Download & install**: it fetches that one installer, checks its length and the
  SHA-256 GitHub publishes with it, closes this copy so the files are free, and starts the
  installer. Never a downgrade, never quiet.
* Join by 8-character code, by scrambled string, by direct address, or through a relay if
  both NATs refuse to cooperate.
* A real tray app: hide and show the window, quit from the icon, log in
  `%APPDATA%\Tandem\tandem.log`.

**Install** — download `tandem-setup.exe` below and run it. Windows may warn about an
unsigned publisher: that is about the certificate, not the contents, and the source is here.

**From a terminal** — `tandem version`, `tandem update`, `tandem update --get`,
`tandem status`, `tandem stop`. `GUIDE.md` is the whole manual and ships with the installer.
