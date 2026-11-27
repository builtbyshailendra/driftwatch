package cli

import (
	"runtime/debug"
	"strings"
)

// ResolveVersion picks the version string the binary reports. A version set
// with -ldflags wins. Otherwise the Go toolchain's build information is used:
// "go install ...@v0.1.0", and "go build" in a checkout of a tagged commit,
// record the module version. Any other build from a Git checkout records the
// commit, reported as "dev+<short hash>", with "-dirty" when the tree had
// local changes. Without either, the version is "dev".
func ResolveVersion(ldflagsVersion string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if ldflagsVersion != "" {
		return ldflagsVersion
	}
	info, ok := readBuildInfo()
	if !ok || info == nil {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" && !isPseudoVersion(v) {
		return strings.TrimSuffix(v, "+dirty")
	}
	var revision string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if revision == "" {
		return "dev"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if dirty {
		revision += "-dirty"
	}
	return "dev+" + revision
}

// isPseudoVersion reports whether v is a Go pseudo-version such as
// v0.1.1-0.20261108093000-7cb5d49a1b2c, which says less than the commit hash
// that follows. The check looks for the 14-digit timestamp and 12-character
// hash that end every pseudo-version, ignoring a "+dirty" suffix.
func isPseudoVersion(v string) bool {
	v = strings.TrimSuffix(v, "+dirty")
	parts := strings.Split(v, "-")
	if len(parts) < 3 {
		return false
	}
	hash, stamp := parts[len(parts)-1], parts[len(parts)-2]
	if i := strings.LastIndex(stamp, "."); i >= 0 {
		stamp = stamp[i+1:]
	}
	return len(hash) == 12 && isDigits(stamp, 14) && isHex(hash)
}

func isDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
