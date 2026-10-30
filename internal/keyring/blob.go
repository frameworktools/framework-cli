package keyring

import (
	"unicode/utf16"
)

// decodeBlob reads a Windows credential blob. R's keyring package stores
// UTF-8, and tools built on the Windows Vault APIs store UTF-16LE. Tokens are
// ASCII, so a blob whose every second byte is zero is UTF-16LE.
func decodeBlob(blob []byte) string {
	if len(blob) == 0 || len(blob)%2 != 0 {
		return string(blob)
	}
	units := make([]uint16, 0, len(blob)/2)
	for i := 0; i < len(blob); i += 2 {
		if blob[i+1] != 0 {
			return string(blob)
		}
		units = append(units, uint16(blob[i]))
	}
	return string(utf16.Decode(units))
}
