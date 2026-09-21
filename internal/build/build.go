// Package build holds values stamped in at link time by GoReleaser.
package build

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// IsRelease reports whether this binary came from a tagged release, as opposed
// to `go run` or a snapshot, which have no meaningful version to compare.
func IsRelease() bool {
	return Version != "dev" && Version != "" && !containsAny(Version, "-next", "-dirty", "SNAPSHOT")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
