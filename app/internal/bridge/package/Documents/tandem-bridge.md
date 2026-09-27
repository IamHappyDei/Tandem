# Tandem Bridge

What this is: a place for the sim to tell Tandem what the aircraft's own switches say, and
for Tandem to put values back.

How to show it in the sim (one line in the aircraft's panel, in its `system.xml` or
`checklist`-free equivalent, inside `<Instruments>`):

    <Instrument id="TANDEM_BRIDGE">
      <Name>Tandem</Name>
      <Mesh>Asobo_DefaultMesh.Billboard_Plane_2x1</Mesh>
      <PanelVirtualCoordinates>
        <LeftOperatingArea>0</LeftOperatingArea>
        <BottomOperatingArea>0</BottomOperatingArea>
        <SizeHorizontaly>512</SizeHorizontaly>
        <SizeVerticaly>256</SizeVerticaly>
      </PanelVirtualCoordinates>
      <HTML>
        <BrowseToURL>tandem-bridge.html</BrowseToURL>
        <DocumentName>Tandem/Bridge/tandem-bridge.html</DocumentName>
      </HTML>
    </Instrument>

Or open it as a 2D window from the aircraft's window list, if that aircraft's windows are
defined in a way that lets you add one.

Tandem's Settings -> Sim bridge prints the address it listens on
(`ws://127.0.0.1:8796/`), the names it wants this aircraft to report, and what arrived last.
