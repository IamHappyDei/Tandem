Tandem 1.2.4 — Browse really browses, and the setup screen is tidier

**Why Browse did nothing.** The click handler had been written *after* the page's closing
`</html>` tag, in one script block the browser never executes, and a stray newline inside a
regular expression broke the half of it that did parse. So the button was there and the folder
window was not. Fixed in both pages: the installer's and the dashboard's. This was never a
mystery — it was a broken file, and I shipped it without looking.

**The picker now opens on top, where you can see it.** It had no owner window, so Windows was
free to drop it behind the setup page. It asks for the process's own visible window and hands
that over as the owner; a test creates a window, opens the dialog, and refuses the release unless
`GetWindow/dialog, GW_OWNER` is that window.

**Paste is gone** — the button, the Ctrl+V handler and the clipboard route, in both pages. The
picker has a path box inside it if you want to type, and the field in the page is a normal field.

**The setup screen got the styles it was already asking for.** The folder row is a labelled field
with the path in monospace, *Browse…* and *Look for it* under it, and a line of status that says
what happened. Enter in the field starts the install.
