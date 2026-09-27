# tandem-sim — the optional SimConnect helper

The **addon is everything Tandem needs**: `app/internal/bridge/package` is Tandem's own
community package, and it talks to the app over `ws://127.0.0.1:8796/` with no other add-on
involved, and no DLL of anybody's.

This helper is the second way in, for when a page cannot be hosted by the aircraft. It is
Tandem's code, written here, and it uses Microsoft's SimConnect client to reach the sim.

## Where the two Microsoft DLLs come from

`SimConnect.dll` and `Microsoft.FlightSimulator.SimConnect.dll` are **not in this repository**
and Tandem does not ship them, borrow them, or look for them in another add-on's folder. To
compile this helper, put a copy from the MSFS 2024 SDK (or from your own sim install) in this
folder, then `dotnet build -c Release`.

At run time the helper looks, in this order, and stops at the first one that exists:

1. next to `tandem-sim.exe` — i.e. inside Tandem's own install folder, your copy;
2. the path you name with `--simconnect C:\...\SimConnect.dll`;
3. the install path the registry records for Microsoft Flight Simulator (2024, then 2020);
4. `Program Files`, then the roots of your fixed drives.

Anything living under a `\Community\` folder, or inside another add-on, is refused out loud —
Tandem does not read a third party's copy of a Microsoft DLL and it does not load code from
somebody else's add-on. That includes this repository's own neighbour folder: `fscopilot/` was
used once while proving the wire protocol and nothing at run time depends on it; delete it and
Tandem cannot tell the difference.
