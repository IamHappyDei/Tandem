Tandem 1.2.1 — the window is GSX again, and it tells the truth about its version

**The version at the bottom of the window was a caption in the page, not a fact.** It read 0.2 on
every machine. The app now stamps its own version into the page as it is served — the same number
`tandem update` compares against — so what you read is what is running.

**The window shows the GSX product only.** "This aircraft" and "the aircraft's own switches" are
hidden: profiles, the sim bridge, its community page and the room are all still there underneath,
still off unless you turn them on by name. The GSX sync switch, Pause, the room and the updater sit
where they always were.

**Two things fixed that 1.2.0 got wrong.** Stopping the network twice — switching shared cockpit
off and then quitting — panicked the process; closing a channel that was already closed is no
longer a fatal error. And a fresh install no longer refuses you the moment you ask for what it
hid: pasting a code, joining a room or asking for an invite switches shared cockpit on and says in
the log that it did, because you cannot share a cockpit without agreeing to the network. Switching
it off again closes the port and leaves the room, and switching it on rejoins.

Getting it: `tandem update`, or the installer below. Checksums are published with the release and
verified before anything is run.
