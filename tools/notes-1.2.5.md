Tandem 1.2.5 — settings come back, and a folder is checked rather than claimed

**Your settings were saved and then not read.** Each cockpit on a PC keeps its own config file,
named after its name and port — correct for running two at once, and a trap the moment you rename
your cockpit in Settings: the next start computed a *different* filename and loaded that instead.
Nothing was lost; it just wasn't the file being read. Now a save writes the shared
`%APPDATA%\Tandem\config.json` too, and a start reads the shared file first, letting the
per-instance file override it. Rename, community folder, switch lists, sim watch list — they come
back on the next start whatever you call the cockpit by then.

**"Found it" now means it looked.** A Community folder path is only called found when add-on
packages are actually recognised underneath it. A guess — or a folder that happens to hold one
stray `manifest.json`, like `C:\Windows` did in testing — is labelled a guess, the box is marked,
and **Browse** stays there for you either way. You can always pick the folder yourself; nothing
fills the field and asks you to trust it.

Same story in the dashboard's Settings: the community field says how many packages it checked
for, warns when it is only a guess, and Browse is never hidden behind a guess.
