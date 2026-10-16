package evidence

import (
	"crypto/md5"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestWrapperPinnedDistribution(t *testing.T) {
	for _, scenario := range []struct {
		tool    string
		windows bool
	}{
		{"gradle", false}, {"maven-only", false}, {"maven-jar", false},
		{"gradle", true}, {"maven-only", true}, {"maven-jar", true},
	} {
		name := scenario.tool
		if scenario.windows {
			name += "_windows_argv"
		}
		t.Run(name, func(t *testing.T) {
			tool := scenario.tool
			windows := scenario.windows || runtime.GOOS == "windows"
			root, home := t.TempDir(), t.TempDir()
			write := func(path, content string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			property, argv := filepath.Join(root, "gradle/wrapper/gradle-wrapper.properties"), []string{"./gradlew", "--offline", "test"}
			pinned := "https://services.gradle.org/distributions/gradle-8.10.2-bin.zip"
			archive := "gradle-8.10.2-bin.zip"
			props, command, launcher := "", "gradle", "lib/gradle-launcher-8.10.2.jar"
			t.Setenv("GRADLE_USER_HOME", home)
			t.Setenv("MAVEN_USER_HOME", home)
			if tool != "gradle" {
				property, argv = filepath.Join(root, ".mvn/wrapper/maven-wrapper.properties"), []string{"./mvnw", "-o", "test"}
				pinned = "https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.9.9/apache-maven-3.9.9-bin.zip"
				archive, command, launcher = "apache-maven-3.9.9-bin.zip", "mvn", "boot/plexus-classworlds-2.8.0.jar"
				if tool == "maven-only" {
					props = "distributionType=only-script\n"
				}
				if tool == "maven-jar" {
					write(filepath.Join(root, ".mvn/wrapper/maven-wrapper.jar"), "wrapper", 0644)
				}
			}
			if windows {
				if tool == "gradle" {
					argv[0] = "./gradlew.bat"
					command = "gradle.bat"
				} else {
					argv[0] = "./mvnw.cmd"
					command = "mvn.cmd"
				}
			}
			properties := props + "distributionUrl=" + pinned + "\n"
			write(property, properties, 0644)
			if why := WrapperUnavailable(root, argv); why == "" {
				t.Fatal("uncached distribution accepted")
			}
			name := strings.TrimSuffix(archive, ".zip")
			var hash string
			switch tool {
			case "gradle":
				sum := md5.Sum([]byte(pinned))
				hash = new(big.Int).SetBytes(sum[:]).Text(36)
			case "maven-only":
				name = strings.TrimSuffix(name, "-bin")
				hash = fmt.Sprintf("%x", javaStringHash(pinned))
				if windows {
					// PowerShell only-mvnw.cmd uses SHA256(URL), not the
					// POSIX script's Java hash (fixed official URL vector).
					hash = "8e74001100ff70d6af083c5511fcc5ec49282d7017cde82c3698eee8fdf86698"
				}
			case "maven-jar":
				u, _ := url.Parse(pinned)
				h, ok := mavenURIHash(u)
				if !ok {
					t.Fatal("plain URL rejected")
				}
				hash = fmt.Sprintf("%x", h)
			}
			cache := filepath.Join(home, "wrapper/dists", name, hash)
			if err := os.MkdirAll(cache, 0755); err != nil {
				t.Fatal(err)
			}
			if why := WrapperUnavailable(root, argv); why == "" {
				t.Fatal("empty pinned cache accepted")
			}
			dist := cache
			if tool != "maven-only" {
				dist = filepath.Join(cache, "distribution")
			}
			launcherMode := os.FileMode(0755)
			if windows {
				launcherMode = 0644
			}
			write(filepath.Join(dist, "bin", command), "cached launcher fixture\n", launcherMode)
			write(filepath.Join(dist, launcher), "launcher", 0644)
			if tool == "gradle" {
				if why := WrapperUnavailable(root, argv); why == "" {
					t.Fatal("Gradle distribution without completion marker accepted")
				}
				write(filepath.Join(cache, archive+".ok"), "", 0644)
			}
			if tool == "maven-jar" {
				if why := WrapperUnavailable(root, argv); why == "" {
					t.Fatal("Java Maven wrapper without retained archive accepted")
				}
				write(filepath.Join(cache, archive), "archive", 0644)
			}
			if why := WrapperUnavailable(root, argv); why != "" {
				t.Fatalf("ready distribution rejected: %s", why)
			}
			if tool != "maven-only" {
				// Java follows directory symlinks when counting distribution
				// roots. A second root invalidates the install and may download.
				link := filepath.Join(cache, "second-root")
				if err := os.Symlink(dist, link); err != nil {
					// Windows may require an unavailable symlink privilege.
					// Other failures are test/environment failures, not skips.
					if runtime.GOOS != "windows" || !errors.Is(err, syscall.Errno(1314)) {
						t.Fatal(err)
					}
					t.Logf("Windows symlink privilege unavailable (ERROR_PRIVILEGE_NOT_HELD): %v", err)
				} else {
					if why := WrapperUnavailable(root, argv); why == "" {
						t.Fatal("extra symlink distribution root accepted")
					}
					if err := os.Remove(link); err != nil {
						t.Fatal(err)
					}
					if why := WrapperUnavailable(root, argv); why != "" {
						t.Fatalf("ready distribution after symlink removal: %s", why)
					}
				}
			}
			write(property, strings.Replace(properties, "https://", "https://mirror.", 1), 0644)
			if why := WrapperUnavailable(root, argv); why == "" {
				t.Fatal("another URL with same archive name reused cache")
			}
			write(property, properties+"distributionPath=custom/cache\n", 0644)
			if why := WrapperUnavailable(root, argv); !strings.Contains(why, "unsupported cache configuration") {
				t.Fatalf("custom path: %s", why)
			}
			write(property, properties, 0644)
			if err := os.Remove(filepath.Join(dist, launcher)); err != nil {
				t.Fatal(err)
			}
			if why := WrapperUnavailable(root, argv); why == "" {
				t.Fatal("partial installation without launcher accepted")
			}
		})
	}
}

func TestWrapperHashCompatibility(t *testing.T) {
	// Known vectors for Java String.hashCode and URI.hashCode of the same URL.
	u, _ := url.Parse("https://example.com/apache-maven-3.9.9-bin.zip")
	if got := javaStringHash(u.String()); got != 0xe4e35196 {
		t.Fatalf("String hash: %x", got)
	}
	if got, ok := mavenURIHash(u); !ok || got != 0x4e607e07 {
		t.Fatalf("URI hash: %x (%t)", got, ok)
	}
}

func TestWrapperWindowsCommandsKeepDownloadGuard(t *testing.T) {
	commands := []string{"./mvnw.cmd", "./gradlew.bat"}
	if runtime.GOOS == "windows" {
		commands = append(commands, `.\MVNW.CMD`, `.\GRADLEW.BAT`)
	}
	for _, command := range commands {
		if why := WrapperUnavailable(t.TempDir(), []string{command, "test"}); why == "" {
			t.Fatalf("uncached known wrapper bypassed download guard: %s", command)
		}
	}
	for _, command := range []string{"./custom.cmd", "./custom.bat", "./mvnw-custom.cmd", "./gradlew-custom.bat", "./mvn.cmd", "./gradle.bat"} {
		if why := WrapperUnavailable(t.TempDir(), []string{command, "test"}); why != "" {
			t.Fatalf("unrelated command classified as wrapper: %s: %s", command, why)
		}
	}
}

func TestWrapperRejectsNonASCIIURLBeforePlatformHash(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".mvn", "wrapper", "maven-wrapper.properties")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("distributionType=only-script\ndistributionUrl=https://example.com/café/apache-maven-3.9.9-bin.zip\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"./mvnw", "./mvnw.cmd"} {
		if why := WrapperUnavailable(root, []string{command, "test"}); !strings.Contains(why, "unsupported non-ASCII") {
			t.Fatalf("%s: %q", command, why)
		}
	}
}

func TestWrapperRejectsDaemonDistributionRewrite(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("MAVEN_USER_HOME", home)
	pinned := "https://example.com/maven-mvnd-1.0.2-bin.zip"
	property := filepath.Join(root, ".mvn", "wrapper", "maven-wrapper.properties")
	if err := os.MkdirAll(filepath.Dir(property), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(property, []byte("distributionType=only-script\ndistributionUrl="+pinned+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// The original URL's fake Maven-shaped cache must not grant readiness:
	// only-mvnw.cmd rewrites mvnd URLs for Windows before hashing them and
	// launches mvnd.cmd, which this guard does not support.
	cache := filepath.Join(home, "wrapper", "dists", "maven-mvnd-1.0.2", "5b7ce2527b19a0cdcaa496ff3142b814ce5d10b81355ef4331be84bbe33038dd")
	for _, file := range []string{"bin/mvn.cmd", "boot/plexus-classworlds-2.8.0.jar"} {
		p := filepath.Join(cache, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("misleading cache"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if why := WrapperUnavailable(root, []string{"./mvnw.cmd", "-o", "test"}); !strings.Contains(why, "unsupported") {
		t.Fatalf("unsupported daemon distribution accepted or mislabeled: %q", why)
	}
}
