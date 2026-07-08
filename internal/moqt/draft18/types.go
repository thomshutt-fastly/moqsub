package draft18

const (
	StreamTypeFetchHeader = 0x05
	StreamTypeSetup       = 0x2F00

	MsgSetup         = 0x2F00
	MsgGoAway        = 0x10
	MsgSubscribe     = 0x03
	MsgSubscribeOK   = 0x04
	MsgRequestError  = 0x05
	MsgRequestOK     = 0x07
	MsgPublish       = 0x1D
	MsgFetch         = 0x16
	MsgFetchOK       = 0x18
	MsgTrackStatus   = 0x0D
	MsgRequestUpdate = 0x02
)

const (
	SetupOptionPath               = 0x01
	SetupOptionAuthorizationToken = 0x03
	SetupOptionMaxAuthTokenCache  = 0x04
	SetupOptionAuthority          = 0x05
	SetupOptionImplementation     = 0x07
)

const (
	ObjectStatusNormal     = 0x00
	ObjectStatusEndGroup   = 0x03
	ObjectStatusEndTrack   = 0x04
	ObjectStatusUnknownMax = 0x04
)

type SetupOption struct {
	Type uint64
	Raw  []byte
}

type SetupMessage struct {
	Options []SetupOption
}

type SubscribeMessage struct {
	RequestID      uint64
	TrackNamespace [][]byte
	TrackName      []byte
	ParametersRaw  []byte
}

// SubscribeOKMessage is the SUBSCRIBE_OK body (draft-18 §10.8). It is sent as a
// response on the same bidirectional request stream as the SUBSCRIBE, so it
// carries no Request ID; the request is implied by the stream.
type SubscribeOKMessage struct {
	TrackAlias      uint64
	NumParams       uint64
	TrackProperties []byte
}

// RequestErrorMessage is the REQUEST_ERROR body (draft-18 §10.6.2). Like
// SUBSCRIBE_OK it is sent on the request stream and carries no Request ID.
type RequestErrorMessage struct {
	Code          uint64
	RetryInterval uint64
	Reason        string
	Redirect      []byte
}

type SubgroupHeader struct {
	Type              uint64
	TrackAlias        uint64
	GroupID           uint64
	SubgroupID        uint64
	HasSubgroupID     bool
	HasExtensions     bool
	UsesFirstObjectID bool
	EndOfGroup        bool
	PublisherPriority uint8
}

type SubgroupObject struct {
	ObjectID       uint64
	ObjectIDDelta  uint64
	ObjectStatus   uint64
	PropertiesRaw  []byte
	Payload        []byte
	PayloadLen     uint64
	IsStatusObject bool
}
