package anypoint

import "testing"

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.10", 1}, {"1.2.3", "1.2.3", 0}, {"1.2", "1.2.0", -1}, {"v2.0.0", "1.99.99", 1},
		{"1.0.0-rc.1", "1.0.0", -1}, {"1.0.0-rc.2", "1.0.0-rc.10", -1}, {"1.0.0-alpha", "1.0.0-1", 1}, {"1.0.0+build", "1.0.0", 0},
	} {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := CompareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}
