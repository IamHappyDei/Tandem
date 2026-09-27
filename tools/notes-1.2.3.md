Tandem 1.2.3 — Browse: the folder picker, like Explorer's

"Where your aircraft live" now opens the **folder picker** — in the installer and in the
dashboard. Navigate the tree, drives and all, click OK, and the path lands in the box. Type or
paste into the box directly if you already know it (there is a box for it inside the dialog too,
and Ctrl+V works there). Cancel says "no folder was picked" instead of filling the box with
nothing. `tandem browse` opens the same dialog from a command window, if you just want to see
where a path really lives.

The picker is tested rather than trusted: a test opens it, closes it, and checks the call comes
back with an honest error instead of hanging, then types a path in, accepts, and checks that
*that* is the path Tandem received.

Nothing else changed. Under the surface 1.2.3 is 1.2.2.
