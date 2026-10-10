package evidence

import (
	"crypto/md5"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// WrapperUnavailable explains why a Gradle or Maven wrapper in dir would
// have to download its pinned distribution, which Radar never lets a run do:
// the wrapper fetches it before the build tool's offline mode applies. It
// reads the committed wrapper properties and the shared user homes as data
// and returns "" when the distribution is already cached.
func WrapperUnavailable(dir string, argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	var properties, homeVar, homeDefault string
	switch filepath.Base(argv[0]) {
	case "gradlew":
		properties, homeVar, homeDefault = filepath.Join("gradle", "wrapper", "gradle-wrapper.properties"), "GRADLE_USER_HOME", ".gradle"
	case "mvnw":
		properties, homeVar, homeDefault = filepath.Join(".mvn", "wrapper", "maven-wrapper.properties"), "MAVEN_USER_HOME", ".m2"
	default:
		return ""
	}
	full, err := pathutil.ResolveInside(dir, filepath.ToSlash(properties))
	if err != nil {
		return "wrapper properties " + filepath.ToSlash(properties) + " are absent"
	}
	data, err := os.ReadFile(full)
	if err != nil || len(data) > 64<<10 {
		return "wrapper properties " + filepath.ToSlash(properties) + " are unreadable"
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.ReplaceAll(strings.TrimSpace(value), `\:`, ":")
		if !ok || strings.ContainsAny(key+value, "\\") {
			return "wrapper properties use unsupported syntax"
		}
		if _, duplicate := props[key]; duplicate {
			return "wrapper properties contain duplicate keys"
		}
		props[key] = value
	}
	distributionURL := props["distributionUrl"]
	u, err := url.Parse(distributionURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || strings.ContainsAny(distributionURL, "%?# \t") {
		return "wrapper distributionUrl is missing or not a plain archive name"
	}
	for _, c := range distributionURL {
		if c > 127 {
			return "wrapper distributionUrl uses unsupported non-ASCII characters"
		}
	}
	archive := filepath.Base(u.Path)
	name := strings.TrimSuffix(archive, ".zip")
	if name == "" || !safeName.MatchString(name) {
		return "wrapper distributionUrl is missing or not a plain archive name"
	}
	if name == archive {
		return "wrapper distributionUrl uses an unsupported archive format"
	}
	for key, expected := range map[string]string{"distributionBase": homeVar, "zipStoreBase": homeVar, "distributionPath": "wrapper/dists", "zipStorePath": "wrapper/dists", "alwaysDownload": "false", "alwaysUnpack": "false"} {
		if v, ok := props[key]; ok && v != expected {
			return "wrapper " + key + " uses unsupported cache configuration"
		}
	}
	home := os.Getenv(homeVar)
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, homeDefault)
		}
	}
	cache := filepath.Join(home, "wrapper", "dists")
	ready := false
	if homeVar == "GRADLE_USER_HOME" {
		// Gradle PathAssembler hashes the pinned URI; Install also requires
		// its completion marker and exactly one usable distribution root.
		sum := md5.Sum([]byte(distributionURL))
		dist := filepath.Join(cache, name, new(big.Int).SetBytes(sum[:]).Text(36))
		ready = regularFile(filepath.Join(dist, archive+".ok")) && distributionRootReady(dist, "gradle")
	} else if props["distributionType"] == "only-script" {
		if strings.Contains(string(data), `\:`) {
			return "wrapper only-script properties use unsupported URL escaping"
		}
		// Apache's only-script wrapper stores the Maven home directly at
		// <archive-without-bin>/<Java String.hashCode(URL)>.
		dist := filepath.Join(cache, strings.TrimSuffix(name, "-bin"), fmt.Sprintf("%x", javaStringHash(distributionURL)))
		ready = executableFile(filepath.Join(dist, "bin", "mvn")) && hasLauncher(dist, "maven")
	} else if props["distributionType"] == "" || props["distributionType"] == "bin" || props["distributionType"] == "source" {
		// JAR-based Apache wrappers use URI.hashCode rather than the shell
		// wrapper's String.hashCode, and retain their downloaded archive.
		jar, jarErr := pathutil.ResolveInside(dir, ".mvn/wrapper/maven-wrapper.jar")
		if jarErr == nil && regularFile(jar) {
			hash, supported := mavenURIHash(u)
			dist := filepath.Join(cache, name, fmt.Sprintf("%x", hash))
			ready = supported && regularFile(filepath.Join(dist, archive)) && distributionRootReady(dist, "maven")
		}
	} else {
		return "wrapper distributionType is unsupported"
	}
	if !ready {
		return "wrapper distribution " + name + " is not cached in " + homeVar + " as a complete pinned distribution and Radar does not download it"
	}
	return ""
}

func javaStringHash(s string) uint32 {
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

// mavenURIHash covers plain HTTP(S) URIs. Unsupported authorities remain
// unavailable instead of guessing which directory the Java wrapper uses.
func mavenURIHash(u *url.URL) (uint32, bool) {
	if strings.ContainsAny(u.Hostname(), "_:[]") || u.Hostname() == "" {
		return 0, false
	}
	port := -1
	if u.Port() != "" {
		var err error
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 0 || port > 65535 {
			return 0, false
		}
	}
	h := javaStringHash(strings.ToLower(u.Scheme))
	h = h*127 + javaStringHash(u.Path)
	for _, c := range strings.ToLower(u.Hostname()) {
		h = h*31 + uint32(c)
	}
	return h + uint32(int32(1949*port)), true
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func hasLauncher(home, tool string) bool {
	pattern := "gradle-launcher-*.jar"
	if tool == "maven" {
		pattern = "plexus-classworlds-*.jar"
	}
	dir := "lib"
	if tool == "maven" {
		dir = "boot"
	}
	files, err := filepath.Glob(filepath.Join(home, dir, pattern))
	return err == nil && len(files) == 1 && regularFile(files[0])
}

func distributionRootReady(dir, tool string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	var home string
	for _, entry := range entries {
		// Java's directory inventory follows symlinks. Refuse them rather
		// than miss an extra root that would make the wrapper reinstall.
		if entry.Type()&os.ModeSymlink != 0 {
			return false
		}
		if entry.IsDir() {
			if home != "" {
				return false
			}
			home = filepath.Join(dir, entry.Name())
		}
	}
	command := "gradle"
	if tool == "maven" {
		command = "mvn"
	}
	return home != "" && executableFile(filepath.Join(home, "bin", command)) && hasLauncher(home, tool)
}
