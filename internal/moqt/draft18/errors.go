package draft18

import "fmt"

// RequestErrorCodeName returns the draft-18 REQUEST_ERROR code name, if known.
// See draft-ietf-moq-transport Section 15.10.2.
func RequestErrorCodeName(code uint64) string {
	switch code {
	case 0x0:
		return "INTERNAL_ERROR"
	case 0x1:
		return "UNAUTHORIZED"
	case 0x2:
		return "TIMEOUT"
	case 0x3:
		return "NOT_SUPPORTED"
	case 0x4:
		return "MALFORMED_AUTH_TOKEN"
	case 0x5:
		return "EXPIRED_AUTH_TOKEN"
	case 0x6:
		return "GOING_AWAY"
	case 0x9:
		return "EXCESSIVE_LOAD"
	case 0x10:
		return "DOES_NOT_EXIST"
	case 0x11:
		return "INVALID_RANGE"
	case 0x12:
		return "MALFORMED_TRACK"
	case 0x19:
		return "DUPLICATE_SUBSCRIPTION"
	case 0x20:
		return "UNINTERESTED"
	case 0x30:
		return "PREFIX_OVERLAP"
	case 0x31:
		return "NAMESPACE_TOO_LARGE"
	case 0x32:
		return "INVALID_JOINING_REQUEST_ID"
	case 0x33:
		return "UNSUPPORTED_EXTENSION"
	case 0x34:
		return "REDIRECT"
	default:
		return fmt.Sprintf("UNKNOWN_0x%x", code)
	}
}
