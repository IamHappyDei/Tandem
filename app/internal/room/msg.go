package room

import "tandem/internal/wire"

type (
	Msg       = wire.Msg
	MenuBrief = wire.MenuBrief
	PeerRef   = wire.PeerRef
)

const ProtoVersion = wire.ProtoVersion

type peerState struct {
	name    string
	origin  string
	lamport int
	since   int64
}
