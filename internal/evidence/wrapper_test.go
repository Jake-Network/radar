package evidence

import (
	"crypto/md5"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrapperPinnedDistribution(t *testing.T) {
	for _, tool := range []string{"gradle", "maven-only", "maven-jar"} {
		t.Run(tool, func(t *testing.T) {
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
			write(filepath.Join(dist, "bin", command), "#!/bin/sh\nexit 0\n", 0755)
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
