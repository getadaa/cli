package update

import "testing"

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.2.0", "1.1.9", true},
		{"v1.10.0", "1.9.0", true},
		{"1.2.0", "1.2.0", false},
		{"1.2.0", "1.2.0-rc.1", true},
		{"1.2.0-rc.1", "1.2.0", false},
		{"0.9.0", "1.0.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestVerify(t *testing.T) {
	archive := []byte("hello")
	sums := []byte("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824  adaa_1.0.0_linux_amd64.tar.gz\n")
	if err := verify(archive, sums, "adaa_1.0.0_linux_amd64.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := verify([]byte("tampered"), sums, "adaa_1.0.0_linux_amd64.tar.gz"); err == nil {
		t.Fatal("a mismatched checksum was accepted")
	}
}
