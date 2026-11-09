package main

import "encoding/binary"

// findSNI, bir TLS ClientHello kaydı içindeki SNI host adının başlangıç
// ofsetini ve uzunluğunu döndürür. Ofset, kayıt başlığı dahil b'nin başından
// sayılır. Yalnızca ilk TLS kaydı incelenir.
func findSNI(b []byte) (off, n int, ok bool) {
	if len(b) < 5 || b[0] != 0x16 {
		return 0, 0, false
	}
	end := 5 + int(binary.BigEndian.Uint16(b[3:5]))
	if end > len(b) {
		end = len(b)
	}

	p := 5
	// Handshake başlığı: tip (1) + uzunluk (3). 0x01 = ClientHello.
	if p+4 > end || b[p] != 0x01 {
		return 0, 0, false
	}
	p += 4
	p += 2 + 32 // client_version + random
	if p+1 > end {
		return 0, 0, false
	}
	p += 1 + int(b[p]) // session_id
	if p+2 > end {
		return 0, 0, false
	}
	p += 2 + int(binary.BigEndian.Uint16(b[p:])) // cipher_suites
	if p+1 > end {
		return 0, 0, false
	}
	p += 1 + int(b[p]) // compression_methods
	if p+2 > end {
		return 0, 0, false
	}
	extEnd := p + 2 + int(binary.BigEndian.Uint16(b[p:]))
	p += 2
	if extEnd > end {
		extEnd = end
	}

	for p+4 <= extEnd {
		typ := binary.BigEndian.Uint16(b[p:])
		l := int(binary.BigEndian.Uint16(b[p+2:]))
		p += 4
		if p+l > extEnd {
			return 0, 0, false
		}
		if typ == 0 { // server_name
			return parseServerName(b, p, p+l)
		}
		p += l
	}
	return 0, 0, false
}

func parseServerName(b []byte, p, end int) (int, int, bool) {
	if p+2 > end {
		return 0, 0, false
	}
	p += 2 // server_name_list uzunluğu
	for p+3 <= end {
		typ := b[p]
		l := int(binary.BigEndian.Uint16(b[p+1:]))
		p += 3
		if p+l > end {
			return 0, 0, false
		}
		if typ == 0 && l > 0 { // host_name
			return p, l, true
		}
		p += l
	}
	return 0, 0, false
}
