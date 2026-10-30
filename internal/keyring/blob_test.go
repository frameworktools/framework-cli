package keyring

import "testing"

func TestDecodeBlob(t *testing.T) {
	cases := map[string][]byte{
		"fw_abc":  []byte("fw_abc"),
		"fw_abcd": {'f', 0, 'w', 0, '_', 0, 'a', 0, 'b', 0, 'c', 0, 'd', 0},
		"ab":      []byte("ab"),
		"":        nil,
	}
	for want, blob := range cases {
		if got := decodeBlob(blob); got != want {
			t.Errorf("decodeBlob(%v) = %q; want %q", blob, got, want)
		}
	}
}

func TestCheckSecret(t *testing.T) {
	for _, ok := range []string{"fw_abc123", "12|AbCdEf"} {
		if err := checkSecret(ok); err != nil {
			t.Errorf("checkSecret(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "has space", "line\nbreak", "é"} {
		if err := checkSecret(bad); err == nil {
			t.Errorf("checkSecret(%q): want an error", bad)
		}
	}
}
