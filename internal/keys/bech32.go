package keys

import "strings"

// This file encodes age's Bech32 form (BIP-173) and never decodes: age's own
// parser checks every string it produces.
const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var bech32Generator = [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}

func bech32Polymod(values []byte) uint32 {
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := range bech32Generator {
			if top>>i&1 == 1 {
				chk ^= bech32Generator[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	ret := make([]byte, 0, 2*len(hrp)+1)
	for _, c := range []byte(hrp) {
		ret = append(ret, c>>5)
	}
	ret = append(ret, 0)
	for _, c := range []byte(hrp) {
		ret = append(ret, c&31)
	}
	return ret
}
func bech32Value(v uint32) byte { return byte(v & 31) }

// bech32ConvertBits regroups 8-bit data into 5-bit groups, padding the tail.
func bech32ConvertBits(data []byte) []byte {
	var ret []byte
	acc, bits := uint32(0), uint(0)
	for _, v := range data {
		acc = acc<<8 | uint32(v)
		bits += 8
		for bits >= 5 {
			bits -= 5
			ret = append(ret, bech32Value(acc>>bits))
		}
	}
	if bits > 0 {
		ret = append(ret, bech32Value(acc<<(5-bits)))
	}
	return ret
}

// bech32Encode returns the lowercase Bech32 encoding of data with hrp.
func bech32Encode(hrp string, data []byte) string {
	h := strings.ToLower(hrp)
	values := bech32ConvertBits(data)
	input := append(bech32HRPExpand(h), values...)
	input = append(input, 0, 0, 0, 0, 0, 0)
	mod := bech32Polymod(input) ^ 1
	var b strings.Builder
	b.WriteString(h)
	b.WriteByte('1')
	for _, v := range values {
		b.WriteByte(bech32Charset[v])
	}
	for p := range 6 {
		b.WriteByte(bech32Charset[bech32Value(mod>>(5*(5-p)))])
	}
	return b.String()
}
