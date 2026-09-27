Tandem 1.2.6 — why the buttons were dead

Not a settings problem, and not a server problem: **the dashboard's script could not be parsed.**
A line that splits text on newlines had lost its escaping and carried a real line break inside a
string, so the browser refused the whole block — every handler in the page with it. The window
loaded, looked calm, and did nothing: *waking up…*, *SOON*, an empty room line, and Settings that
appeared not to save because nothing ran to save it. Two of my recent edits did this (the folder
field, then the watch-list box), and I shipped both without checking that the page still parsed.

Fixed by spelling the newline in a way no edit can eat, in both places. Same defect found and
fixed in the installer's page earlier.

**And it cannot happen quietly again:** the suite now pulls the script out of both pages and runs
`node --check` on it, so a page that would load dead fails the build instead of reaching you.

```
ok   internal/ui/web/index.html parses (1 script block)
ok   cmd/tandem-setup/web/index.html parses (1 script block)
```

Settings themselves were fine — and in 1.2.5 they got the fix they needed (a rename no longer
loses your config). Everything else in this build is 1.2.5.
