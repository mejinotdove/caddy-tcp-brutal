package tcpbrutal

import "encoding/binary"

const TCP_BRUTAL_PARAMS = 23301

func encodeParams(rate uint64, cwnd uint32, group uint64) []byte {
	size := 12
	if group != 0 {
		size = 20
	}
	payload := make([]byte, size)
	binary.NativeEndian.PutUint64(payload[0:8], rate)
	binary.NativeEndian.PutUint32(payload[8:12], cwnd)
	if group != 0 {
		binary.NativeEndian.PutUint64(payload[12:20], group)
	}
	return payload
}
