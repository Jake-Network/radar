package testselection

import (
	"path"
	"regexp"
	"strings"
)

// javaRun is how one JVM test class runs under its build tool. It is derived
// from literal build files during discovery; no build script is evaluated.
type javaRun struct {
	cwd    string   // Maven reactor or Gradle settings root
	runner []string // build tool and fixed flags
	module []string // Maven -pl/-am flags or the Gradle test task path
	class  string   // fully qualified test class
	junit  string   // CWD-relative report directory the build tool writes
}

var (
	javaPackageDecl = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	javaTestMarker  = regexp.MustCompile(`@(?:Test|ParameterizedTest|RepeatedTest|TestFactory|TestTemplate)\b|\bextends\s+TestCase\b`)
	xmlComment      = regexp.MustCompile(`(?s)<!--.*?-->`)
	mavenProfiles   = regexp.MustCompile(`(?s)<profiles>.*?</profiles>`)
	mavenModule     = regexp.MustCompile(`<module>\s*([^<]*?)\s*</module>`)
	gradleInclude   = regexp.MustCompile(`\binclude\b`)
	gradleLiteral   = regexp.MustCompile(`^(?:'([^'$\\]*)'|"([^"$\\]*)")$`)
	blockComment    = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment     = regexp.MustCompile(`(?m)//.*$`)
)

var gradleFiles = []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}

// javaTestFile follows Surefire's default class patterns and the test source
// directory convention, and requires a test annotation or a JUnit 3 base
// outside comments, so helpers and fixtures under src/test/java are not
// inventoried as tests. The match is lexical: a marker in a string counts.
func javaTestFile(file, content string) bool {
	base := strings.TrimSuffix(path.Base(file), ".java")
	conventional := strings.Contains("/"+file, "/src/test/java/") || strings.HasPrefix(base, "Test") || strings.HasSuffix(base, "Test") || strings.HasSuffix(base, "Tests") || strings.HasSuffix(base, "TestCase") || strings.HasSuffix(base, "IT")
	return conventional && javaTestMarker.MatchString(withoutComments(content))
}

// withoutComments drops // and /* */ comments for lexical checks; comment
// markers inside string literals are not distinguished.
func withoutComments(content string) string {
	return lineComment.ReplaceAllString(blockComment.ReplaceAllString(content, ""), "")
}

// javaPackage reads the package declaration lexically, outside comments; the
// empty string is the unnamed package.
func javaPackage(content string) string {
	if m := javaPackageDecl.FindStringSubmatch(withoutComments(content)); m != nil {
		return m[1]
	}
	return ""
}

func javaClass(file, content string) string {
	class := strings.TrimSuffix(path.Base(file), ".java")
	if pkg := javaPackage(content); pkg != "" {
		return pkg + "." + class
	}
	return class
}

func hasAny(contents map[string]string, dir string, names ...string) bool {
	for _, name := range names {
		if _, ok := contents[path.Join(dir, name)]; ok {
			return true
		}
	}
	return false
}

// javaBuild classifies a Java test by its nearest build file and derives how
// it runs. It returns the framework, the module (Maven) or project (Gradle)
// directory, and either a run or the reason none is safely known.
func javaBuild(file, content string, contents map[string]string) (string, string, *javaRun, string) {
	for dir := path.Dir(file); ; dir = path.Dir(dir) {
		maven, gradle := hasAny(contents, dir, "pom.xml"), hasAny(contents, dir, gradleFiles...)
		switch {
		case maven && gradle:
			return "java-unknown", dir, nil, "both pom.xml and a Gradle build file in " + dir + "; the build tool is ambiguous"
		case maven:
			if reason := outsideTestSources(file, dir); reason != "" {
				return "maven", dir, nil, reason
			}
			run := mavenRun(dir, contents)
			run.class = javaClass(file, content)
			return "maven", dir, run, ""
		case gradle:
			project, run, reason := gradleRun(file, dir, contents)
			if run != nil {
				if reason = outsideTestSources(file, project); reason != "" {
					return "gradle", project, nil, reason
				}
				run.class = javaClass(file, content)
			}
			return "gradle", project, run, reason
		}
		if dir == "." {
			return "java-unknown", ".", nil, "no pom.xml or Gradle build file encloses this Java test"
		}
	}
}

// outsideTestSources explains why a test outside the module's src/test/java
// cannot be selected: Surefire and Gradle's test task compile only that
// default source set, and a filter naming an uncompiled class finds nothing.
func outsideTestSources(file, module string) string {
	if strings.HasPrefix(file, path.Join(module, "src", "test", "java")+"/") {
		return ""
	}
	return "outside " + path.Join(module, "src", "test", "java") + ", the only test sources the default build compiles"
}

// relativeTo returns p relative to its ancestor dir.
func relativeTo(dir, p string) string {
	if dir == "." {
		return p
	}
	if p == dir {
		return "."
	}
	return strings.TrimPrefix(p, dir+"/")
}

// mavenModules lists the literal <module> entries active by default:
// commented-out modules and profile-only modules are not built by `mvn test`.
func mavenModules(pom string) []string {
	pom = mavenProfiles.ReplaceAllString(xmlComment.ReplaceAllString(pom, ""), "")
	var out []string
	for _, m := range mavenModule.FindAllStringSubmatch(pom, -1) {
		module := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(m[1], "./"), "/pom.xml"), "/")
		if module == "" || strings.Contains(module, "${") {
			continue
		}
		out = append(out, path.Clean(module))
	}
	return out
}

// mavenRun runs a module from the outermost reactor that literally lists it
// (each step through an ancestor's <modules>), so sibling modules it depends
// on build in the same offline invocation (-pl MODULE -am).
func mavenRun(module string, contents map[string]string) *javaRun {
	root := module
	for climbed := true; climbed && root != "."; {
		climbed = false
		for dir := path.Dir(root); ; dir = path.Dir(dir) {
			if pom, ok := contents[path.Join(dir, "pom.xml")]; ok && contains(mavenModules(pom), relativeTo(dir, root)) {
				root, climbed = dir, true
				break
			}
			if dir == "." {
				break
			}
		}
	}
	runner := "mvn"
	if hasAny(contents, root, "mvnw") {
		runner = "./mvnw"
	}
	run := &javaRun{cwd: root, runner: []string{runner, "-o", "-B"}, junit: path.Join(relativeTo(root, module), "target", "surefire-reports")}
	if root != module {
		run.module = []string{"-pl", relativeTo(root, module), "-am"}
	}
	return run
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

// gradleSettings maps literal include("a:b") project directories to project
// paths. Uncertain is set when settings compute or relocate projects, which
// only evaluating the script could resolve.
type gradleSettings struct {
	projects  map[string]string // directory relative to the settings root -> project path
	uncertain bool
}

func parseGradleSettings(content string) gradleSettings {
	s := gradleSettings{projects: map[string]string{}}
	content = withoutComments(content)
	if strings.Contains(content, "projectDir") || strings.Contains(content, "includeFlat") {
		s.uncertain = true
	}
	for _, loc := range gradleInclude.FindAllStringIndex(content, -1) {
		rest := strings.TrimLeft(content[loc[1]:], " \t")
		var args string
		if strings.HasPrefix(rest, "(") {
			end := strings.Index(rest, ")")
			if end < 0 {
				s.uncertain = true
				continue
			}
			args = rest[1:end]
		} else {
			// Groovy: include 'a', 'b' continues over trailing commas.
			for {
				line, next, more := strings.Cut(rest, "\n")
				args += line
				if !more || !strings.HasSuffix(strings.TrimSpace(line), ",") {
					break
				}
				rest = next
			}
		}
		for _, arg := range strings.Split(args, ",") {
			arg = strings.TrimSpace(arg)
			if arg == "" {
				continue
			}
			m := gradleLiteral.FindStringSubmatch(arg)
			if m == nil {
				s.uncertain = true
				continue
			}
			name := strings.Trim(m[1]+m[2], ":")
			if name == "" {
				s.uncertain = true
				continue
			}
			s.projects[strings.ReplaceAll(name, ":", "/")] = ":" + name
		}
	}
	return s
}

// gradleRun targets the test task of the project that owns the file. The
// project comes from the nearest enclosing settings file; a root-level
// `test` task would run every project's tests, and a filter matching no test
// fails each project without one.
func gradleRun(file, buildDir string, contents map[string]string) (string, *javaRun, string) {
	settingsRoot, settings := "", ""
	for dir := buildDir; ; dir = path.Dir(dir) {
		for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
			if content, ok := contents[path.Join(dir, name)]; ok && settingsRoot == "" {
				settingsRoot, settings = dir, content
			}
		}
		if settingsRoot != "" || dir == "." {
			break
		}
	}
	if settingsRoot == "" {
		// A build without settings is a single root project.
		settingsRoot = buildDir
	}
	parsed := parseGradleSettings(settings)
	project, projectPath := settingsRoot, ":"
	for dir := path.Dir(file); dir != settingsRoot && dir != "." && dir != "/"; dir = path.Dir(dir) {
		if p, ok := parsed.projects[relativeTo(settingsRoot, dir)]; ok {
			project, projectPath = dir, p
			break
		}
	}
	if parsed.uncertain {
		return project, nil, "Gradle settings in " + settingsRoot + " compute or relocate projects; the owning project is not known without evaluating them"
	}
	if project == settingsRoot && buildDir != settingsRoot {
		return buildDir, nil, "Gradle build file in " + buildDir + " is not a literally included project of " + settingsRoot
	}
	runner := "gradle"
	if hasAny(contents, settingsRoot, "gradlew") {
		runner = "./gradlew"
	}
	task := projectPath + ":test"
	if projectPath == ":" {
		task = ":test"
	}
	return project, &javaRun{cwd: settingsRoot, runner: []string{runner, "--offline", "--no-daemon", "--console=plain"}, module: []string{task}, junit: path.Join(relativeTo(settingsRoot, project), "build", "test-results", "test")}, ""
}

// argv returns the targeted command for the run's test class, or the whole
// module's suite when full is set.
func (r *javaRun) argv(framework string, full bool) []string {
	argv := append(append([]string(nil), r.runner...), r.module...)
	switch framework {
	case "maven":
		if !full {
			argv = append(argv, "-Dsurefire.failIfNoSpecifiedTests=false", "-DfailIfNoTests=false", "-Dtest="+r.class)
		}
		return append(argv, "test")
	case "gradle":
		if !full {
			argv = append(argv, "--tests", r.class)
		}
		return argv
	}
	return nil
}

// javaClasses splits a targeted JVM build command into its fixed arguments
// and selected test classes, for grouping one module's classes into one run.
func javaClasses(c Command) ([]string, []string, bool) {
	var fixed, classes []string
	switch c.Framework {
	case "maven":
		for _, a := range c.Command {
			if v, ok := strings.CutPrefix(a, "-Dtest="); ok {
				classes = append(classes, strings.Split(v, ",")...)
				continue
			}
			fixed = append(fixed, a)
		}
	case "gradle":
		for i := 0; i < len(c.Command); i++ {
			if c.Command[i] == "--tests" && i+1 < len(c.Command) {
				classes = append(classes, c.Command[i+1])
				i++
				continue
			}
			fixed = append(fixed, c.Command[i])
		}
	default:
		return nil, nil, false
	}
	return fixed, classes, len(classes) > 0
}

func withJavaClasses(framework string, fixed, classes []string) []string {
	argv := append([]string(nil), fixed...)
	switch framework {
	case "maven":
		// The selector precedes the trailing "test" phase.
		last := argv[len(argv)-1]
		return append(append(argv[:len(argv)-1], "-Dtest="+strings.Join(classes, ",")), last)
	case "gradle":
		for _, class := range classes {
			argv = append(argv, "--tests", class)
		}
	}
	return argv
}

// javaToolAvailable requires a JVM and either the committed wrapper or the
// build tool on PATH. Cached dependencies are not verified.
func javaToolAvailable(argv []string, available func(string) bool) bool {
	if len(argv) == 0 || !available("java") {
		return false
	}
	return strings.HasPrefix(argv[0], "./") || available(argv[0])
}
